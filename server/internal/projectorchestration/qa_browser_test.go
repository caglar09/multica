package projectorchestration

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserQA_DetectsConsoleCrash(t *testing.T) {
	outputDir := t.TempDir()
	writeBrowserQAFixture(t, outputDir, browserQAReport{
		Version: 1, Status: "failed",
		ConsoleErrors:  []string{"TypeError: Cannot read properties of undefined (reading 'name')"},
		Screenshots:    []string{"qa-0001.jpg"},
		ExecutionTrace: []string{"opened http://127.0.0.1:4173/form", "browser QA failed: uncaught page error"},
		Error:          "browser QA detected console or network failures",
	})

	result, err := readBrowserQAResult(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.ConsoleErrors) != 1 || !strings.Contains(result.ConsoleErrors[0], "TypeError") {
		t.Fatalf("console crash did not fail browser QA: %+v", result)
	}
	if len(result.Screenshots) != 1 || result.Screenshots[0].ContentType != "image/jpeg" {
		t.Fatalf("console crash did not preserve screenshot evidence: %+v", result.Screenshots)
	}
	image, err := base64.StdEncoding.DecodeString(result.Screenshots[0].DataBase64)
	if err != nil || len(image) == 0 || len(result.Screenshots[0].SHA256) != 64 {
		t.Fatalf("screenshot evidence is incomplete: bytes=%d digest=%q err=%v", len(image), result.Screenshots[0].SHA256, err)
	}
}

func TestBrowserQA_SuccessfulJourney(t *testing.T) {
	scenario := QAScenario{
		TargetURL:      "http://127.0.0.1:4173/login",
		PreviewCommand: []string{"corepack", "pnpm", "dev", "--host", "127.0.0.1"},
		Steps: []string{
			"fill input[name=email] = qa@example.invalid",
			"fill input[name=password] = test-only-password",
			"click button[type=submit]",
			"assert-url /dashboard",
			"assert-visible h1",
		},
		Viewport:           QAViewport{Width: 1280, Height: 720},
		CaptureScreenshots: true,
	}
	if err := validateBrowserQAScenario(scenario); err != nil {
		t.Fatalf("form and redirect scenario was rejected: %v", err)
	}

	outputDir := t.TempDir()
	writeBrowserQAFixture(t, outputDir, browserQAReport{
		Version: 1, Status: "passed", Screenshots: []string{"qa-0001.jpg", "qa-0002.jpg"},
		ExecutionTrace: []string{
			"opened http://127.0.0.1:4173/login",
			"step 1: fill passed", "step 2: fill passed", "step 3: click passed",
			"step 4: assert-url passed", "step 5: assert-visible passed",
		},
	})
	result, err := readBrowserQAResult(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || len(result.Screenshots) != 2 ||
		!strings.Contains(result.ExecutionTrace, "step 4: assert-url passed") ||
		!strings.Contains(result.ExecutionTrace, "step 5: assert-visible passed") {
		t.Fatalf("successful form and redirect evidence was rejected: %+v", result)
	}
	evidence := browserQAResultEvidence(scenario, result)
	if evidence["network_disabled"] != true {
		t.Fatalf("browser QA evidence omitted isolation proof: %+v", evidence)
	}
	qualityResult := QualityGateResult{Passed: true, Evidence: map[string]any{"browser_qa": evidence}}
	before := qualityResultDigest(qualityResult)
	screenshots := append([]QAScreenshot(nil), result.Screenshots...)
	screenshots[0].DataBase64 += "tampered"
	qualityResult.Evidence["browser_qa"].(map[string]any)["screenshots"] = screenshots
	if after := qualityResultDigest(qualityResult); after == before {
		t.Fatal("quality result digest did not bind the screenshot artifact bytes")
	}
}

func TestBrowserQA_RejectsExternalTargetsAndMissingCriticalUIAssertion(t *testing.T) {
	base := QAScenario{
		TargetURL:      "https://example.com",
		PreviewCommand: []string{"corepack", "pnpm", "dev"},
		Steps:          []string{"assert-visible h1"},
		Viewport:       QAViewport{Width: 1280, Height: 720},
	}
	if err := validateBrowserQAScenario(base); err == nil {
		t.Fatal("external browser target was accepted")
	}
	base.TargetURL = "http://127.0.0.1:3000"
	base.Steps = []string{"click button[type=submit]", "assert-url /done"}
	if err := validateBrowserQAScenario(base); err == nil || !strings.Contains(err.Error(), "assert-visible") {
		t.Fatalf("scenario without a critical UI assertion was accepted: %v", err)
	}
}

func TestBrowserQA_PassingReportWithoutScreenshotFailsClosed(t *testing.T) {
	outputDir := t.TempDir()
	writeBrowserQAFixture(t, outputDir, browserQAReport{
		Version: 1, Status: "passed", ExecutionTrace: []string{"opened", "step 1: assert-visible passed"},
	})
	result, err := readBrowserQAResult(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || !strings.Contains(result.Error, "without complete") {
		t.Fatalf("report without screenshot passed: %+v", result)
	}
}

func writeBrowserQAFixture(t *testing.T, outputDir string, report browserQAReport) {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "result.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range report.Screenshots {
		if err := os.WriteFile(filepath.Join(outputDir, name), []byte{0xff, 0xd8, 0xff, 0xd9}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
