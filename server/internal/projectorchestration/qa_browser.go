package projectorchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	qualityPlaywrightImage          = "multica-quality-playwright:1.56.1-pnpm-10.28.2"
	maxBrowserQAScenarioBytes       = 64 * 1024
	maxBrowserQASteps               = 30
	maxBrowserQAScreenshotCount     = 6
	maxBrowserQAScreenshotSize      = 1024 * 1024
	maxBrowserQAScreenshotTotalSize = 3 * 1024 * 1024
	maxBrowserQAResultSize          = 128 * 1024
)

var browserQAScreenshotName = regexp.MustCompile(`^qa-[0-9]{4}\.jpg$`)

// BrowserQARunner executes a deterministic browser journey in an isolated
// preview environment. DaemonQualityGateRunner is the production implementation.
type BrowserQARunner interface {
	RunUserJourney(ctx context.Context, scenario QAScenario) (QAResult, error)
}

func browserQACommand() daemonQualityCommand {
	return daemonQualityCommand{
		name: "node", args: []string{"/opt/multica/qa-runner.cjs", "/input/browser-qa-scenario.json", "/tmp/browser-qa-output"},
		image: qualityPlaywrightImage, project: "playwright", display: "playwright chromium acceptance journey",
	}
}

// QAScenario is supplied in QualityGateRequest.Artifact under "browser_qa".
// Steps use the small selector-based DSL documented in validateBrowserQAStep.
type QAScenario struct {
	TargetURL          string     `json:"target_url"`
	PreviewCommand     []string   `json:"preview_command"`
	Steps              []string   `json:"steps"`
	Viewport           QAViewport `json:"viewport"`
	CaptureScreenshots bool       `json:"capture_screenshots"`
}

type QAViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// QAScreenshot contains reviewable image bytes as part of the gate evidence.
// Path is a stable artifact name, never a host or worktree path.
type QAScreenshot struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
	DataBase64  string `json:"data_base64"`
}

type QAResult struct {
	Passed          bool           `json:"passed"`
	ConsoleErrors   []string       `json:"console_errors"`
	NetworkFailures []string       `json:"network_failures"`
	Screenshots     []QAScreenshot `json:"screenshots"`
	ExecutionTrace  string         `json:"execution_trace"`
	Error           string         `json:"error,omitempty"`
}

type browserQAReport struct {
	Version         int      `json:"version"`
	Status          string   `json:"status"`
	ConsoleErrors   []string `json:"console_errors"`
	NetworkFailures []string `json:"network_failures"`
	Screenshots     []string `json:"screenshots"`
	ExecutionTrace  []string `json:"execution_trace"`
	Error           string   `json:"error,omitempty"`
}

func browserQAScenarioFromArtifact(gateType string, artifact map[string]any) (QAScenario, bool, error) {
	value, present := artifact["browser_qa"]
	if !present {
		return QAScenario{}, false, nil
	}
	if !strings.EqualFold(gateType, "acceptance") {
		return QAScenario{}, true, errors.New("browser QA scenarios are only valid for acceptance gates")
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxBrowserQAScenarioBytes {
		return QAScenario{}, true, errors.New("browser QA scenario is malformed or exceeds its size limit")
	}
	var input struct {
		TargetURL          string      `json:"target_url"`
		PreviewCommand     []string    `json:"preview_command"`
		Steps              []string    `json:"steps"`
		Viewport           *QAViewport `json:"viewport"`
		CaptureScreenshots *bool       `json:"capture_screenshots"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return QAScenario{}, true, fmt.Errorf("decode browser QA scenario: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return QAScenario{}, true, errors.New("browser QA scenario contains trailing data")
	}
	scenario := QAScenario{
		TargetURL:          strings.TrimSpace(input.TargetURL),
		PreviewCommand:     append([]string(nil), input.PreviewCommand...),
		Steps:              append([]string(nil), input.Steps...),
		Viewport:           QAViewport{Width: 1280, Height: 720},
		CaptureScreenshots: true,
	}
	if input.Viewport != nil {
		scenario.Viewport = *input.Viewport
	}
	if input.CaptureScreenshots != nil {
		scenario.CaptureScreenshots = *input.CaptureScreenshots
	}
	if err := validateBrowserQAScenario(scenario); err != nil {
		return QAScenario{}, true, err
	}
	return scenario, true, nil
}

func validateBrowserQAScenario(scenario QAScenario) error {
	target, err := url.Parse(scenario.TargetURL)
	if err != nil || target.Scheme != "http" || target.User != nil || !isBrowserQALoopbackHost(target.Hostname()) {
		return errors.New("browser QA target must be an HTTP loopback preview URL")
	}
	if port := target.Port(); port != "" {
		portNumber, parseErr := strconv.Atoi(port)
		if parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return errors.New("browser QA target has an invalid preview port")
		}
	}
	if len(scenario.PreviewCommand) == 0 || len(scenario.PreviewCommand) > 16 {
		return errors.New("browser QA requires a bounded preview command")
	}
	command := strings.TrimSpace(scenario.PreviewCommand[0])
	switch command {
	case "corepack", "pnpm", "npm", "node":
	default:
		return errors.New("browser QA preview command must start with corepack, pnpm, npm, or node")
	}
	commandBytes := 0
	for _, argument := range scenario.PreviewCommand {
		if argument == "" || strings.ContainsRune(argument, '\x00') || len(argument) > 2048 {
			return errors.New("browser QA preview command contains an invalid argument")
		}
		commandBytes += len(argument)
	}
	if commandBytes > 8*1024 {
		return errors.New("browser QA preview command exceeds its size limit")
	}
	if len(scenario.Steps) == 0 || len(scenario.Steps) > maxBrowserQASteps {
		return errors.New("browser QA requires between 1 and 30 user journey steps")
	}
	hasCriticalUIAssertion := false
	for index, step := range scenario.Steps {
		if len(step) > 2048 {
			return fmt.Errorf("browser QA step %d exceeds its size limit", index+1)
		}
		if err := validateBrowserQAStep(step); err != nil {
			return fmt.Errorf("browser QA step %d: %w", index+1, err)
		}
		if strings.HasPrefix(strings.TrimSpace(step), "assert-visible ") {
			hasCriticalUIAssertion = true
		}
	}
	if !hasCriticalUIAssertion {
		return errors.New("browser QA requires at least one assert-visible step for critical UI")
	}
	if scenario.Viewport.Width < 320 || scenario.Viewport.Width > 3840 ||
		scenario.Viewport.Height < 320 || scenario.Viewport.Height > 2160 {
		return errors.New("browser QA viewport is outside the supported bounds")
	}
	return nil
}

func isBrowserQALoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

// Browser QA steps are deliberately declarative and selector based:
//   - click <Playwright selector>
//   - fill <Playwright selector> = <value>
//   - assert-visible <Playwright selector>
//   - assert-hidden <Playwright selector>
//   - assert-text <Playwright selector> = <expected text>
//   - assert-url </path?query> (exact path, query, and fragment after redirect)
func validateBrowserQAStep(step string) error {
	step = strings.TrimSpace(step)
	for _, prefix := range []string{"click ", "assert-visible ", "assert-hidden "} {
		if strings.HasPrefix(step, prefix) && strings.TrimSpace(strings.TrimPrefix(step, prefix)) != "" {
			return nil
		}
	}
	for _, prefix := range []string{"fill ", "assert-text "} {
		if strings.HasPrefix(step, prefix) {
			rest := strings.TrimSpace(strings.TrimPrefix(step, prefix))
			selector, value, found := strings.Cut(rest, "=")
			if found && strings.TrimSpace(selector) != "" && strings.TrimSpace(value) != "" {
				return nil
			}
			return errors.New("step must use '<selector> = <value>'")
		}
	}
	if strings.HasPrefix(step, "assert-url ") {
		expected := strings.TrimSpace(strings.TrimPrefix(step, "assert-url "))
		if expected == "" || !strings.HasPrefix(expected, "/") || strings.HasPrefix(expected, "//") {
			return errors.New("assert-url requires a local absolute path such as /dashboard")
		}
		if strings.ContainsAny(expected, "\r\n\x00") {
			return errors.New("assert-url contains invalid characters")
		}
		return nil
	}
	return errors.New("unsupported step; use click, fill, assert-visible, assert-hidden, assert-text, or assert-url")
}

func prepareBrowserQAArtifacts(tempDir string, scenario QAScenario) (string, error) {
	encoded, err := json.Marshal(scenario)
	if err != nil {
		return "", fmt.Errorf("encode browser QA scenario: %w", err)
	}
	if len(encoded) > maxBrowserQAScenarioBytes {
		return "", errors.New("browser QA scenario exceeds its size limit")
	}
	configPath := filepath.Join(tempDir, "browser-qa-scenario.json")
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		return "", fmt.Errorf("write isolated browser QA scenario: %w", err)
	}
	if err := os.Chmod(configPath, 0o644); err != nil {
		return "", fmt.Errorf("make browser QA scenario readable in container: %w", err)
	}
	outputDir := filepath.Join(tempDir, "browser-qa-output")
	if err := os.Mkdir(outputDir, 0o733); err != nil {
		return "", fmt.Errorf("create browser QA artifact directory: %w", err)
	}
	if err := os.Chmod(outputDir, 0o733); err != nil {
		return "", fmt.Errorf("prepare browser QA artifact directory: %w", err)
	}
	return outputDir, nil
}

func readBrowserQAResult(outputDir string) (QAResult, error) {
	result := QAResult{ConsoleErrors: []string{}, NetworkFailures: []string{}, Screenshots: []QAScreenshot{}}
	info, err := os.Lstat(outputDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("browser QA artifact directory is missing or invalid")
	}
	resultPath := filepath.Join(outputDir, "result.json")
	fileInfo, err := os.Lstat(resultPath)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() > maxBrowserQAResultSize {
		return result, errors.New("browser QA report is missing or exceeds its size limit")
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return result, fmt.Errorf("read browser QA report: %w", err)
	}
	var report browserQAReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return result, fmt.Errorf("decode browser QA report: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return result, errors.New("browser QA report contains trailing data")
	}
	if report.Version != 1 || (report.Status != "passed" && report.Status != "failed") {
		return result, errors.New("browser QA report has an unsupported version or status")
	}
	if len(report.ConsoleErrors) > 100 || len(report.NetworkFailures) > 100 ||
		len(report.ExecutionTrace) > maxBrowserQASteps*4 || len(report.Screenshots) > maxBrowserQAScreenshotCount {
		return result, errors.New("browser QA report exceeds its evidence limits")
	}
	result.ConsoleErrors = boundedBrowserQALines(report.ConsoleErrors, 100, 4096)
	result.NetworkFailures = boundedBrowserQALines(report.NetworkFailures, 100, 4096)
	trace := boundedBrowserQALines(report.ExecutionTrace, maxBrowserQASteps*4, 4096)
	result.ExecutionTrace = strings.Join(trace, "\n")
	result.Error = boundedQualityString(report.Error, 4096)
	seen := make(map[string]struct{}, len(report.Screenshots))
	totalSize := 0
	for _, name := range report.Screenshots {
		if !browserQAScreenshotName.MatchString(name) {
			return result, errors.New("browser QA report contains an invalid screenshot name")
		}
		if _, exists := seen[name]; exists {
			return result, errors.New("browser QA report contains a duplicate screenshot")
		}
		seen[name] = struct{}{}
		path := filepath.Join(outputDir, name)
		imageInfo, err := os.Lstat(path)
		if err != nil || !imageInfo.Mode().IsRegular() || imageInfo.Size() < 4 || imageInfo.Size() > maxBrowserQAScreenshotSize {
			return result, fmt.Errorf("browser QA screenshot %q is missing or exceeds its size limit", name)
		}
		totalSize += int(imageInfo.Size())
		if totalSize > maxBrowserQAScreenshotTotalSize {
			return result, errors.New("browser QA screenshots exceed their total size limit")
		}
		image, err := os.ReadFile(path)
		if err != nil {
			return result, fmt.Errorf("read browser QA screenshot %q: %w", name, err)
		}
		if !bytes.HasPrefix(image, []byte{0xff, 0xd8, 0xff}) || !bytes.HasSuffix(image, []byte{0xff, 0xd9}) {
			return result, fmt.Errorf("browser QA screenshot %q is not a JPEG artifact", name)
		}
		digest := sha256.Sum256(image)
		result.Screenshots = append(result.Screenshots, QAScreenshot{
			Path: name, ContentType: "image/jpeg", SHA256: hex.EncodeToString(digest[:]),
			DataBase64: base64.StdEncoding.EncodeToString(image),
		})
	}
	result.Passed = report.Status == "passed" && len(result.ConsoleErrors) == 0 &&
		len(result.NetworkFailures) == 0 && result.Error == "" && len(result.Screenshots) > 0
	if report.Status == "passed" && !result.Passed {
		result.Error = "browser QA report claims success without complete, clean browser evidence"
	}
	if report.Status == "failed" && result.Error == "" {
		result.Error = "browser QA journey failed"
	}
	return result, nil
}

func boundedBrowserQALines(lines []string, countLimit, lineLimit int) []string {
	if len(lines) > countLimit {
		lines = lines[:countLimit]
	}
	bounded := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			bounded = append(bounded, boundedQualityString(line, lineLimit))
		}
	}
	return bounded
}

func browserQAResultEvidence(scenario QAScenario, result QAResult) map[string]any {
	target, _ := url.Parse(scenario.TargetURL)
	return map[string]any{
		"passed":               result.Passed,
		"target_origin":        target.Scheme + "://" + target.Host,
		"viewport":             scenario.Viewport,
		"step_count":           len(scenario.Steps),
		"console_errors":       result.ConsoleErrors,
		"network_failures":     result.NetworkFailures,
		"screenshots":          result.Screenshots,
		"execution_trace":      result.ExecutionTrace,
		"error":                result.Error,
		"network_disabled":     true,
		"preview_in_container": true,
	}
}
