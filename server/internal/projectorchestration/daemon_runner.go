package projectorchestration

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

const (
	defaultDaemonQualityTimeout = 5 * time.Minute
	maxDaemonQualityOutput      = 64 * 1024
	qualityRuntimeLogLimit      = ((maxDaemonQualityOutput+2)/3)*4*2 + 4096
	maxDaemonQualityArchive     = 1024 * 1024 * 1024
	qualityOCIStopTimeout       = 2 * time.Second
	qualityOCICleanupTimeout    = 10 * time.Second
	qualityOCIMemoryLimit       = "2g"
	qualityOCITmpLimit          = "2g"
	qualityOCICPUQuota          = "2"
	qualityOCIPIDLimit          = "128"
	qualityGoImage              = "golang:1.27.3-alpine"
	qualityNodeImage            = "multica-quality-node:22.23.4-pnpm-10.28.2"
	qualityPNPMVersion          = "10.28.2"
)

var (
	gitCommitSHA       = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
	digestSHA256       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	containerImageID   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	containerIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{12,64}$`)
)

// DaemonQualityGateRunner runs fixed quality commands in a networkless OCI
// container. It starts only from the exact committed tree recorded for a task.
type DaemonQualityGateRunner struct {
	timeout     time.Duration
	runtimePath string // Set only by package tests to provide a fake OCI runtime.
}

func NewDaemonQualityGateRunner() QualityGateRunner {
	return &DaemonQualityGateRunner{timeout: defaultDaemonQualityTimeout}
}

type daemonQualityCommand struct {
	name    string
	args    []string
	image   string
	project string
	display string
}

type daemonQualityProjectKind string

const (
	projectGo   daemonQualityProjectKind = "go"
	projectNode daemonQualityProjectKind = "node"
	projectRust daemonQualityProjectKind = "rust"
)

type boundedQualityOutput struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (b *boundedQualityOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(p)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		b.data = append(b.data, p[:remaining]...)
	}
	if written > remaining {
		b.truncated = true
	}
	return written, nil
}

func (r *DaemonQualityGateRunner) Run(ctx context.Context, request QualityGateRequest) (QualityGateResult, error) {
	result := QualityGateResult{
		RunnerType:   "daemon_oci",
		TaskID:       util.UUIDToString(request.TaskID),
		GateType:     request.GateType,
		WorktreePath: request.WorktreePath,
		RepoPath:     request.RepoPath,
		CommitSHA:    request.CommitSHA,
	}
	if !request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.PlanID.Valid ||
		!request.NodeID.Valid || !request.IssueID.Valid || !request.TaskID.Valid {
		return daemonQualityFailure(result, "daemon quality gate requires complete task identity", nil)
	}
	if !gitCommitSHA.MatchString(request.CommitSHA) {
		return daemonQualityFailure(result, "daemon quality gate requires a full recorded commit SHA", nil)
	}
	worktree, repo, err := validateQualityWorktree(ctx, request.WorktreePath, request.RepoPath, request.CommitSHA)
	if err != nil {
		return daemonQualityFailure(result, err.Error(), nil)
	}
	command, err := detectDaemonQualityCommand(worktree, request.GateType, request.CommitSHA)
	if err != nil {
		return daemonQualityFailure(result, err.Error(), map[string]any{
			"repo_path":  repo,
			"commit_sha": request.CommitSHA,
		})
	}
	result.ExecutedCommand = command.display

	runtimePath, runtimeName, err := r.ociRuntime()
	if err != nil {
		return daemonQualityFailure(result, err.Error(), map[string]any{
			"repo_path":        repo,
			"container_image":  command.image,
			"network_disabled": true,
		})
	}
	tempDir, err := os.MkdirTemp("", "multica-quality-")
	if err != nil {
		return daemonQualityFailure(result, fmt.Sprintf("create quality runner temp directory: %v", err), nil)
	}
	defer os.RemoveAll(tempDir)
	for _, name := range []string{"docker-config", "xdg-config"} {
		if err := os.MkdirAll(filepath.Join(tempDir, name), 0o700); err != nil {
			return daemonQualityFailure(result, fmt.Sprintf("create isolated OCI config directory: %v", err), nil)
		}
	}

	imageID, err := inspectQualityImage(runtimePath, runtimeName, tempDir, command.image)
	if err != nil {
		return daemonQualityFailure(result, err.Error(), map[string]any{
			"repo_path":         repo,
			"container_image":   command.image,
			"container_runtime": runtimeName,
			"network_disabled":  true,
		})
	}
	result.Evidence = map[string]any{
		"runner_type":                result.RunnerType,
		"task_id":                    result.TaskID,
		"gate_type":                  result.GateType,
		"executed_command":           result.ExecutedCommand,
		"worktree_path":              result.WorktreePath,
		"repo_path":                  result.RepoPath,
		"commit_sha":                 result.CommitSHA,
		"container_runtime":          runtimeName,
		"container_image":            command.image,
		"container_image_id":         imageID,
		"network_disabled":           true,
		"host_environment_inherited": false,
		"task_worktree_mounted":      false,
		"source_snapshot":            "git_archive_of_recorded_commit",
		"resource_limits": map[string]string{
			"memory": qualityOCIMemoryLimit,
			"cpus":   qualityOCICPUQuota,
			"pids":   qualityOCIPIDLimit,
			"tmpfs":  qualityOCITmpLimit,
		},
	}
	if command.project == "node" {
		version, preflightErr := preflightQualityPNPM(runtimePath, runtimeName, tempDir, imageID)
		if preflightErr != nil {
			return daemonQualityFailure(result, preflightErr.Error(), result.Evidence)
		}
		result.Evidence["package_manager_version"] = version
	}

	snapshotPath := filepath.Join(tempDir, "task.tar")
	snapshotDigest, err := archiveQualityCommit(ctx, worktree, request.CommitSHA, snapshotPath)
	if err != nil {
		return daemonQualityFailure(result, err.Error(), result.Evidence)
	}
	result.Evidence["source_snapshot_digest"] = snapshotDigest
	containerPath, err := filepath.Abs(snapshotPath)
	if err != nil {
		return daemonQualityFailure(result, "resolve quality snapshot path", result.Evidence)
	}
	if strings.Contains(containerPath, ",") {
		return daemonQualityFailure(result, "quality snapshot path contains an unsupported OCI mount separator", result.Evidence)
	}

	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultDaemonQualityTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cidFile := filepath.Join(tempDir, "container.cid")
	startedAt := time.Now()
	startErr := startQualityContainer(runCtx, runtimePath, runtimeName, tempDir, cidFile, containerPath, imageID, command, request)
	if startErr != nil {
		if runCtx.Err() != nil {
			startErr = fmt.Errorf("quality container start exceeded %s", timeout)
		} else {
			startErr = fmt.Errorf("start isolated quality container: %w", startErr)
		}
		containerID := readQualityContainerID(cidFile)
		if containerID != "" {
			_ = stopAndRemoveQualityContainer(runtimePath, runtimeName, tempDir, containerID)
		}
		return daemonQualityFailure(result, startErr.Error(), result.Evidence)
	}
	containerID := readQualityContainerID(cidFile)
	if containerID == "" {
		return daemonQualityFailure(result, "OCI runtime started a quality container without a valid container ID", result.Evidence)
	}

	waitOutput, waitErr := runQualityRuntimeCommand(runCtx, runtimePath, runtimeName, tempDir, []string{"wait", containerID}, 1024)
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	if timedOut || errors.Is(runCtx.Err(), context.Canceled) {
		_ = stopQualityContainer(runtimePath, runtimeName, tempDir, containerID)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), qualityOCICleanupTimeout)
		waitOutput, _ = runQualityRuntimeCommand(cleanupCtx, runtimePath, runtimeName, tempDir, []string{"wait", containerID}, 1024)
		cleanupCancel()
	} else if waitErr != nil {
		_ = stopQualityContainer(runtimePath, runtimeName, tempDir, containerID)
	}
	containerExit, parseWaitErr := parseQualityContainerExit(waitOutput)
	if parseWaitErr == nil {
		result.ExitCode = &containerExit
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), qualityOCICleanupTimeout)
	logs, logsErr := runQualityRuntimeCommand(cleanupCtx, runtimePath, runtimeName, tempDir, []string{"logs", containerID}, qualityRuntimeLogLimit)
	cleanupCancel()
	_ = removeQualityContainer(runtimePath, runtimeName, tempDir, containerID)
	var logEvidenceErr error
	if logsErr == nil {
		stdout, stderr, commandExit, stdoutSize, stderrSize, parseErr := parseDaemonQualityLogs(logs)
		if parseErr == nil {
			result.Stdout = stdout
			result.Stderr = stderr
			result.OutputTruncated = stdoutSize > maxDaemonQualityOutput || stderrSize > maxDaemonQualityOutput
			result.TestCount, result.FailedCount = parseGoTestCounts(result.Stdout + "\n" + result.Stderr)
			if result.ExitCode == nil {
				result.ExitCode = &commandExit
			} else if *result.ExitCode != commandExit {
				logEvidenceErr = errors.New("OCI wait status does not match the recorded quality command exit code")
			}
		} else if !timedOut && runCtx.Err() == nil {
			logEvidenceErr = fmt.Errorf("malformed quality container output evidence: %w", parseErr)
		}
	} else if !timedOut && runCtx.Err() == nil {
		return daemonQualityFailure(result, fmt.Sprintf("collect quality container output: %v", logsErr), result.Evidence)
	}
	result.DurationMs = time.Since(startedAt).Milliseconds()

	if timedOut {
		return daemonQualityFailure(result, fmt.Sprintf("quality command exceeded %s", timeout), result.Evidence)
	}
	if runCtx.Err() != nil {
		return daemonQualityFailure(result, "quality command was canceled", result.Evidence)
	}
	if parseWaitErr != nil {
		return daemonQualityFailure(result, fmt.Sprintf("malformed OCI wait status: %v", parseWaitErr), result.Evidence)
	}
	if waitErr != nil {
		return daemonQualityFailure(result, fmt.Sprintf("wait for quality container: %v", waitErr), result.Evidence)
	}
	if logsErr != nil {
		return daemonQualityFailure(result, fmt.Sprintf("collect quality container output: %v", logsErr), result.Evidence)
	}
	if logEvidenceErr != nil {
		return daemonQualityFailure(result, logEvidenceErr.Error(), result.Evidence)
	}
	if _, _, err := validateQualityWorktree(ctx, worktree, repo, request.CommitSHA); err != nil {
		return daemonQualityFailure(result, fmt.Sprintf("task commit changed during quality execution: %v", err), result.Evidence)
	}
	result.Passed = result.ExitCode != nil && *result.ExitCode == 0
	if !result.Passed {
		result.Error = "quality command did not exit successfully"
	}
	result.Evidence = qualityResultEvidence(result, result.Evidence)
	result.ArtifactDigest = daemonQualityDigest(result)
	result.Evidence["artifact_digest"] = result.ArtifactDigest
	if !result.Passed {
		return result, nil
	}
	return result, nil
}

func daemonQualityFailure(result QualityGateResult, message string, details map[string]any) (QualityGateResult, error) {
	result.Passed = false
	result.Error = boundedQualityString(message, 4096)
	result.Evidence = qualityResultEvidence(result, details)
	result.ArtifactDigest = daemonQualityDigest(result)
	result.Evidence["artifact_digest"] = result.ArtifactDigest
	result.Evidence["error"] = result.Error
	return result, errors.New(result.Error)
}

func qualityResultEvidence(result QualityGateResult, details map[string]any) map[string]any {
	evidence := make(map[string]any, len(details)+14)
	for key, value := range details {
		evidence[key] = value
	}
	evidence["runner_type"] = result.RunnerType
	evidence["task_id"] = result.TaskID
	evidence["gate_type"] = result.GateType
	if result.ExecutedCommand != "" {
		evidence["executed_command"] = result.ExecutedCommand
	}
	evidence["exit_code"] = result.ExitCode
	evidence["worktree_path"] = result.WorktreePath
	evidence["repo_path"] = result.RepoPath
	evidence["commit_sha"] = result.CommitSHA
	evidence["duration_ms"] = result.DurationMs
	evidence["stdout"] = boundedQualityString(result.Stdout, maxDaemonQualityOutput)
	evidence["stderr"] = boundedQualityString(result.Stderr, maxDaemonQualityOutput)
	evidence["output_truncated"] = result.OutputTruncated
	evidence["test_count"] = result.TestCount
	evidence["failed_count"] = result.FailedCount
	if result.Error != "" {
		evidence["error"] = boundedQualityString(result.Error, 4096)
	}
	return evidence
}

func (r *DaemonQualityGateRunner) ociRuntime() (string, string, error) {
	if r.runtimePath != "" {
		path, err := filepath.Abs(r.runtimePath)
		if err != nil {
			return "", "", fmt.Errorf("resolve OCI runtime path: %w", err)
		}
		name := filepath.Base(path)
		if name != "docker" && name != "podman" {
			return "", "", errors.New("quality gate OCI runtime must be Docker or Podman")
		}
		return path, name, nil
	}
	for _, name := range []string{"docker", "podman"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, name, nil
		}
	}
	return "", "", errors.New("isolated quality gate requires an available Docker or Podman OCI runtime")
}

func inspectQualityImage(runtimePath, runtimeName, tempDir, image string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := runQualityRuntimeCommand(ctx, runtimePath, runtimeName, tempDir,
		[]string{"image", "inspect", "--format", "{{.Id}}", image}, 4096)
	if err != nil {
		return "", fmt.Errorf("required quality image %q is not available locally (image pulls are disabled): %w", image, err)
	}
	id := strings.TrimSpace(string(output))
	if !containerImageID.MatchString(id) {
		return "", fmt.Errorf("OCI runtime returned a malformed image ID for %q", image)
	}
	return id, nil
}

func preflightQualityPNPM(runtimePath, runtimeName, tempDir, imageID string) (string, error) {
	args := []string{
		"run", "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--pids-limit=" + qualityOCIPIDLimit,
		"--memory=" + qualityOCIMemoryLimit, "--cpus=" + qualityOCICPUQuota,
		"--tmpfs", "/tmp:rw,nosuid,nodev,exec,mode=1777,size=" + qualityOCITmpLimit,
		"--user=65532:65532", "--env=HOME=/tmp/home", "--env=TMPDIR=/tmp",
		"--env=COREPACK_HOME=/opt/corepack", "--env=COREPACK_ENABLE_NETWORK=0",
		"--env=COREPACK_DEFAULT_TO_LATEST=0",
		"--env=PATH=/usr/local/go/bin:/usr/local/cargo/bin:/usr/local/bin:/usr/bin:/bin",
		"--entrypoint=/bin/sh", imageID, "-c", "corepack pnpm --version",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := runQualityRuntimeCommand(ctx, runtimePath, runtimeName, tempDir, args, 4096)
	if err != nil {
		return "", fmt.Errorf("quality gate unavailable: pinned pnpm %s preflight failed (offline dependencies are unavailable): %w: %s",
			qualityPNPMVersion, err, boundedQualityString(string(output), 2048))
	}
	version := strings.TrimSpace(string(output))
	if version != qualityPNPMVersion {
		return "", fmt.Errorf("quality gate unavailable: pinned pnpm preflight returned %q; want %q", version, qualityPNPMVersion)
	}
	return version, nil
}

func startQualityContainer(ctx context.Context, runtimePath, runtimeName, tempDir, cidFile, snapshotPath, imageID string, command daemonQualityCommand, request QualityGateRequest) error {
	if strings.Contains(cidFile, ",") || strings.Contains(snapshotPath, ",") {
		return errors.New("quality runner temporary path contains an unsupported OCI mount separator")
	}
	args := []string{
		"run", "--detach", "--cidfile", cidFile, "--pull=never",
		"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--pids-limit=" + qualityOCIPIDLimit, "--memory=" + qualityOCIMemoryLimit,
		"--cpus=" + qualityOCICPUQuota,
		"--tmpfs", "/tmp:rw,nosuid,nodev,exec,mode=1777,size=" + qualityOCITmpLimit,
		"--user=65532:65532", "--workdir=/tmp",
		"--mount=type=bind,source=" + snapshotPath + ",target=/input/task.tar,readonly",
		"--label=com.multica.quality-gate.task=" + request.TaskID.String(),
		"--label=com.multica.quality-gate.commit=" + request.CommitSHA,
		"--env=HOME=/tmp/home", "--env=TMPDIR=/tmp", "--env=TMP=/tmp", "--env=TEMP=/tmp",
		"--env=PATH=/usr/local/go/bin:/usr/local/cargo/bin:/usr/local/bin:/usr/bin:/bin",
		"--env=CI=1", "--env=NO_COLOR=1", "--env=TERM=dumb",
		"--env=GOCACHE=/tmp/go-cache", "--env=GOMODCACHE=/tmp/go-mod-cache",
		"--env=GOPROXY=off", "--env=GOSUMDB=off", "--env=GOTOOLCHAIN=local",
		"--env=COREPACK_HOME=/opt/corepack", "--env=COREPACK_ENABLE_NETWORK=0",
		"--env=COREPACK_DEFAULT_TO_LATEST=0",
		"--env=npm_config_offline=true", "--env=npm_config_userconfig=/dev/null",
		"--entrypoint=/bin/sh", imageID, "-c", qualityContainerScript, "quality-gate",
	}
	args = append(args, command.name)
	args = append(args, command.args...)
	output, err := runQualityRuntimeCommand(ctx, runtimePath, runtimeName, tempDir, args, 16*1024)
	if err != nil {
		if len(output) > 0 {
			return fmt.Errorf("%w: %s", err, boundedQualityString(string(output), 4096))
		}
		return err
	}
	return nil
}

const qualityContainerScript = `set -u
mkdir -p /tmp/home /tmp/go-cache /tmp/go-mod-cache /tmp/task
tar -xf /input/task.tar -C /tmp/task || exit 125
cd /tmp/task || exit 125
"$@" >/tmp/quality.stdout 2>/tmp/quality.stderr
status=$?
stdout_size=$(wc -c </tmp/quality.stdout | tr -d '[:space:]')
stderr_size=$(wc -c </tmp/quality.stderr | tr -d '[:space:]')
printf '__MC_RESULT__%s:%s:%s\n' "$status" "$stdout_size" "$stderr_size"
head -c 65536 /tmp/quality.stdout | base64 | tr -d '\n'
printf '\n__MC_STDERR__\n'
head -c 65536 /tmp/quality.stderr | base64 | tr -d '\n'
printf '\n__MC_DONE__\n'
exit "$status"
`

func runQualityRuntimeCommand(ctx context.Context, runtimePath, runtimeName, tempDir string, args []string, outputLimit int) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, runtimePath, args...)
	cmd.Env = qualityRuntimeEnvironment(tempDir)
	output := &boundedQualityOutput{limit: outputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return output.data, fmt.Errorf("%s %s failed: %w", runtimeName, strings.Join(args, " "), err)
	}
	if output.truncated {
		return output.data, errors.New("OCI runtime output exceeded its capture limit")
	}
	return output.data, nil
}

func qualityRuntimeEnvironment(tempDir string) []string {
	return []string{
		"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin:/opt/homebrew/sbin",
		"HOME=" + tempDir,
		"TMPDIR=" + tempDir,
		"TMP=" + tempDir,
		"TEMP=" + tempDir,
		"DOCKER_CONFIG=" + filepath.Join(tempDir, "docker-config"),
		"XDG_CONFIG_HOME=" + filepath.Join(tempDir, "xdg-config"),
	}
}

func archiveQualityCommit(ctx context.Context, worktree, commitSHA, archivePath string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	archiveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create quality snapshot: %w", err)
	}
	defer file.Close()
	cmd := exec.CommandContext(archiveCtx, "git", "-C", worktree, "archive", "--format=tar", commitSHA)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin:/opt/homebrew/sbin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	output := &boundedQualityOutput{limit: 4096}
	cmd.Stdout = &limitedArchiveWriter{writer: file, limit: maxDaemonQualityArchive}
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		_ = os.Remove(archivePath)
		return "", fmt.Errorf("create exact-commit quality snapshot: %w: %s", err, boundedQualityString(string(output.data), 2048))
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close quality snapshot: %w", err)
	}
	if err := os.Chmod(archivePath, 0o444); err != nil {
		return "", fmt.Errorf("make quality snapshot read-only: %w", err)
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return "", fmt.Errorf("inspect quality snapshot: %w", err)
	}
	if info.Size() > maxDaemonQualityArchive {
		return "", fmt.Errorf("quality commit snapshot exceeds %d-byte limit", maxDaemonQualityArchive)
	}
	if err := validateQualityArchive(archivePath, worktree, commitSHA); err != nil {
		return "", err
	}
	snapshot, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("read quality snapshot: %w", err)
	}
	defer snapshot.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, snapshot); err != nil {
		return "", fmt.Errorf("hash quality snapshot: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateQualityArchive(archivePath, worktree, commitSHA string) error {
	// git archive intentionally honors export-ignore/export-subst; either would
	// make the test input differ from the committed source tree, so fail closed.
	attrs, err := qualityGitOutput(context.Background(), worktree, "grep", "-I", "-e", "export-ignore", "-e", "export-subst", commitSHA, "--", ".gitattributes", ":(glob)**/.gitattributes")
	if err == nil && strings.TrimSpace(attrs) != "" {
		return errors.New("quality snapshot is unsafe because committed .gitattributes uses export-ignore or export-subst")
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return fmt.Errorf("inspect committed archive attributes: %w", err)
		}
	}

	listed, err := qualityGitBytes(worktree, "ls-tree", "-r", "-z", "--name-only", commitSHA)
	if err != nil {
		return fmt.Errorf("list tracked quality snapshot files: %w", err)
	}
	tracked := make(map[string]struct{})
	for _, name := range strings.Split(string(listed), "\x00") {
		if name != "" {
			tracked[name] = struct{}{}
		}
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open quality snapshot: %w", err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	archived := make(map[string]struct{}, len(tracked))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("validate quality snapshot archive: %w", err)
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA || header.Typeflag == tar.TypeSymlink {
			archived[header.Name] = struct{}{}
		}
	}
	if len(archived) != len(tracked) {
		return errors.New("quality snapshot file list does not match the recorded commit")
	}
	for name := range tracked {
		if _, ok := archived[name]; !ok {
			return errors.New("quality snapshot file list does not match the recorded commit")
		}
	}
	return nil
}

var errQualityArchiveTooLarge = errors.New("quality commit snapshot exceeds its byte limit")

type limitedArchiveWriter struct {
	writer  io.Writer
	limit   int64
	written int64
}

func (w *limitedArchiveWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, errQualityArchiveTooLarge
	}
	if int64(len(p)) > remaining {
		n, err := w.writer.Write(p[:remaining])
		w.written += int64(n)
		if err != nil {
			return n, err
		}
		return n, errQualityArchiveTooLarge
	}
	n, err := w.writer.Write(p)
	w.written += int64(n)
	return n, err
}

func qualityGitBytes(dir string, args ...string) ([]byte, error) {
	program, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git is unavailable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gitArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, program, gitArgs...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin:/opt/homebrew/sbin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	output := &boundedQualityOutput{limit: 16 * 1024 * 1024}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if output.truncated {
		return nil, errors.New("git quality snapshot metadata exceeds its capture limit")
	}
	return output.data, nil
}

func validateQualityWorktree(ctx context.Context, worktreePath, repoPath, commitSHA string) (string, string, error) {
	worktree, err := filepath.Abs(strings.TrimSpace(worktreePath))
	if err != nil || strings.TrimSpace(worktreePath) == "" {
		return "", "", errors.New("recorded task worktree path is invalid")
	}
	worktree, err = filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", "", fmt.Errorf("resolve task worktree: %w", err)
	}
	repo, err := filepath.Abs(strings.TrimSpace(repoPath))
	if err != nil || strings.TrimSpace(repoPath) == "" {
		return "", "", errors.New("recorded repository path is invalid")
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return "", "", fmt.Errorf("resolve recorded repository: %w", err)
	}
	worktreeRoot, worktreeCommonDir, err := inspectQualityGitPath(ctx, worktree)
	if err != nil {
		return "", "", fmt.Errorf("inspect task worktree: %w", err)
	}
	repoRoot, repoCommonDir, err := inspectQualityGitPath(ctx, repo)
	if err != nil {
		return "", "", fmt.Errorf("inspect recorded repository: %w", err)
	}
	if worktreeRoot != worktree || repoRoot != repo || worktreeRoot == repoRoot || worktreeCommonDir != repoCommonDir {
		return "", "", errors.New("recorded task path is not an isolated worktree of the recorded repository")
	}
	head, err := qualityGitOutput(ctx, worktree, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("read task worktree commit: %w", err)
	}
	if !strings.EqualFold(head, commitSHA) {
		return "", "", errors.New("task worktree HEAD does not match the recorded commit SHA")
	}
	status, err := qualityGitOutput(ctx, worktree, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", "", fmt.Errorf("inspect task worktree changes: %w", err)
	}
	if status != "" {
		return "", "", errors.New("task worktree is dirty; quality evidence must match its recorded commit")
	}
	return worktree, repoRoot, nil
}

func inspectQualityGitPath(ctx context.Context, path string) (string, string, error) {
	root, err := qualityGitOutput(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", "", err
	}
	commonDir, err := qualityGitOutput(ctx, path, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	commonDir, err = filepath.EvalSymlinks(filepath.Clean(commonDir))
	if err != nil {
		return "", "", err
	}
	return filepath.Clean(root), filepath.Clean(commonDir), nil
}

func qualityGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := qualityGitBytesBounded(ctx, dir, 64*1024, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func qualityGitBytesBounded(ctx context.Context, dir string, outputLimit int, args ...string) ([]byte, error) {
	program, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git is unavailable: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	gitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gitArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(gitCtx, program, gitArgs...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin:/opt/homebrew/sbin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	output := &boundedQualityOutput{limit: outputLimit}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if output.truncated {
		return nil, errors.New("git quality metadata exceeded its capture limit")
	}
	return output.data, nil
}

func detectDaemonQualityCommand(root, gateType, commitSHA string) (daemonQualityCommand, error) {
	if !gitCommitSHA.MatchString(commitSHA) {
		return daemonQualityCommand{}, errors.New("quality command detection requires a full commit SHA")
	}
	paths, err := qualityGitBytesBounded(context.Background(), root, 64*1024, "rev-list", "--parents", "-n", "1", commitSHA)
	if err != nil {
		return daemonQualityCommand{}, fmt.Errorf("inspect quality commit parents: %w", err)
	}
	commitParts := strings.Fields(string(paths))
	if len(commitParts) == 0 || !strings.EqualFold(commitParts[0], commitSHA) {
		return daemonQualityCommand{}, errors.New("quality commit metadata is malformed")
	}
	diffArgs := []string{"diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z"}
	if len(commitParts) > 1 {
		diffArgs = append(diffArgs, commitParts[1], commitSHA)
	} else {
		diffArgs = append(diffArgs, "--root", commitSHA)
	}
	changed, err := qualityGitBytesBounded(context.Background(), root, 64*1024, diffArgs...)
	if err != nil {
		return daemonQualityCommand{}, fmt.Errorf("inspect files in quality commit: %w", err)
	}
	if len(changed) == 0 {
		return daemonQualityCommand{}, errors.New("quality commit contains no changed files")
	}
	kinds := make(map[daemonQualityProjectKind]struct{})
	moduleRoots := map[daemonQualityProjectKind]string{}
	for _, path := range strings.Split(string(changed), "\x00") {
		if path == "" {
			continue
		}
		kind, moduleRoot, err := qualityProjectForChangedPath(root, path)
		if err != nil {
			return daemonQualityCommand{}, err
		}
		if kind == "" {
			continue
		}
		if existing, ok := moduleRoots[kind]; ok && existing != moduleRoot {
			return daemonQualityCommand{}, fmt.Errorf("quality commit changes multiple %s projects; command selection is ambiguous", kind)
		}
		moduleRoots[kind] = moduleRoot
		kinds[kind] = struct{}{}
	}
	if len(kinds) != 1 {
		return daemonQualityCommand{}, errors.New("quality commit must target exactly one supported project type")
	}
	var kind daemonQualityProjectKind
	for selected := range kinds {
		kind = selected
	}
	argsFor := func(kind daemonQualityProjectKind, moduleRoot, image, name string, args ...string) daemonQualityCommand {
		if moduleRoot != "." {
			if kind == projectGo {
				args = append([]string{"-C", moduleRoot}, args...)
			} else if kind == projectNode {
				args = append([]string{args[0], "--dir", moduleRoot}, args[1:]...)
				name = "corepack"
			}
		}
		return daemonQualityCommand{project: string(kind), image: image, name: name, args: args, display: strings.Join(append([]string{name}, args...), " ")}
	}
	moduleRoot := moduleRoots[kind]
	switch gateType {
	case "unit_test", "integration_test", "acceptance":
		switch kind {
		case projectGo:
			return argsFor(projectGo, moduleRoot, qualityGoImage, "go", "test", "-v", "./..."), nil
		case projectNode:
			return argsFor(projectNode, moduleRoot, qualityNodeImage, "corepack", "pnpm", "test"), nil
		case projectRust:
			return daemonQualityCommand{}, errors.New("Cargo quality gates are unavailable because no pinned offline OCI image is configured")
		}
	case "build", "build_verification":
		switch kind {
		case projectGo:
			return argsFor(projectGo, moduleRoot, qualityGoImage, "go", "build", "./..."), nil
		case projectNode:
			return argsFor(projectNode, moduleRoot, qualityNodeImage, "corepack", "pnpm", "run", "build"), nil
		case projectRust:
			return daemonQualityCommand{}, errors.New("Cargo quality gates are unavailable because no pinned offline OCI image is configured")
		}
	case "lint":
		switch kind {
		case projectGo:
			return argsFor(projectGo, moduleRoot, qualityGoImage, "go", "vet", "./..."), nil
		case projectNode:
			return argsFor(projectNode, moduleRoot, qualityNodeImage, "corepack", "pnpm", "lint"), nil
		}
	}
	return daemonQualityCommand{}, fmt.Errorf("no allowlisted local command for gate %q and detected project type %q", gateType, kind)
}

func qualityProjectForChangedPath(root, path string) (kind daemonQualityProjectKind, moduleRoot string, err error) {
	relative := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("quality commit contains an invalid changed path %q", path)
	}
	switch strings.ToLower(filepath.Ext(relative)) {
	case ".md", ".mdx", ".rst", ".adoc":
		return "", "", nil
	}
	manifestFor := func(project daemonQualityProjectKind) string {
		switch project {
		case projectGo:
			return "go.mod"
		case projectNode:
			return "package.json"
		default:
			return "Cargo.toml"
		}
	}
	start := filepath.Dir(filepath.Join(root, relative))
	for dir := start; ; dir = filepath.Dir(dir) {
		var found daemonQualityProjectKind
		for _, project := range []daemonQualityProjectKind{projectGo, projectNode, projectRust} {
			if _, statErr := os.Stat(filepath.Join(dir, manifestFor(project))); statErr == nil {
				if found != "" {
					return "", "", fmt.Errorf("quality project root %q has multiple language manifests", dir)
				}
				found = project
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return "", "", fmt.Errorf("inspect project manifest in %q: %w", dir, statErr)
			}
		}
		if found != "" {
			relModuleRoot, relErr := filepath.Rel(root, dir)
			if relErr != nil {
				return "", "", relErr
			}
			return found, filepath.ToSlash(relModuleRoot), nil
		}
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", "", fmt.Errorf("quality commit changed %q outside a detected Go, Node, or Rust project", path)
}

func readQualityContainerID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if !containerIDPattern.MatchString(id) {
		return ""
	}
	return id
}

func parseQualityContainerExit(output []byte) (int, error) {
	status, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || status < 0 || status > 255 {
		return 0, errors.New("invalid container exit code")
	}
	return status, nil
}

func parseDaemonQualityLogs(logs []byte) (stdout, stderr string, exitCode, stdoutSize, stderrSize int, err error) {
	const resultPrefix = "__MC_RESULT__"
	const stderrSeparator = "\n__MC_STDERR__\n"
	const doneMarker = "\n__MC_DONE__\n"
	start := strings.Index(string(logs), resultPrefix)
	if start < 0 {
		return "", "", 0, 0, 0, errors.New("missing quality result header")
	}
	logText := string(logs[start:])
	headerEnd := strings.IndexByte(logText, '\n')
	if headerEnd < 0 {
		return "", "", 0, 0, 0, errors.New("incomplete quality result header")
	}
	header := strings.TrimPrefix(logText[:headerEnd], resultPrefix)
	parts := strings.Split(header, ":")
	if len(parts) != 3 {
		return "", "", 0, 0, 0, errors.New("malformed quality result header")
	}
	if exitCode, err = strconv.Atoi(parts[0]); err != nil || exitCode < 0 || exitCode > 255 {
		return "", "", 0, 0, 0, errors.New("malformed quality command exit code")
	}
	if stdoutSize, err = strconv.Atoi(parts[1]); err != nil || stdoutSize < 0 {
		return "", "", 0, 0, 0, errors.New("malformed quality stdout size")
	}
	if stderrSize, err = strconv.Atoi(parts[2]); err != nil || stderrSize < 0 {
		return "", "", 0, 0, 0, errors.New("malformed quality stderr size")
	}
	stdoutStart := headerEnd + 1
	relativeSeparator := strings.Index(logText[stdoutStart:], stderrSeparator)
	if relativeSeparator < 0 {
		return "", "", 0, 0, 0, errors.New("missing quality stderr separator")
	}
	stdoutEnd := stdoutStart + relativeSeparator
	stderrStart := stdoutEnd + len(stderrSeparator)
	relativeDone := strings.Index(logText[stderrStart:], doneMarker)
	if relativeDone < 0 {
		return "", "", 0, 0, 0, errors.New("missing quality completion marker")
	}
	stderrEnd := stderrStart + relativeDone
	stdoutData, err := base64.StdEncoding.DecodeString(strings.TrimSpace(logText[stdoutStart:stdoutEnd]))
	if err != nil {
		return "", "", 0, 0, 0, fmt.Errorf("decode quality stdout: %w", err)
	}
	stderrData, err := base64.StdEncoding.DecodeString(strings.TrimSpace(logText[stderrStart:stderrEnd]))
	if err != nil {
		return "", "", 0, 0, 0, fmt.Errorf("decode quality stderr: %w", err)
	}
	if len(stdoutData) != min(stdoutSize, maxDaemonQualityOutput) || len(stderrData) != min(stderrSize, maxDaemonQualityOutput) {
		return "", "", 0, 0, 0, errors.New("quality output sizes do not match captured bytes")
	}
	return string(stdoutData), string(stderrData), exitCode, stdoutSize, stderrSize, nil
}

func stopQualityContainer(runtimePath, runtimeName, tempDir, containerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), qualityOCICleanupTimeout)
	defer cancel()
	_, err := runQualityRuntimeCommand(ctx, runtimePath, runtimeName, tempDir, []string{"stop", "--time", strconv.Itoa(int(qualityOCIStopTimeout.Seconds())), containerID}, 4096)
	return err
}

func removeQualityContainer(runtimePath, runtimeName, tempDir, containerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), qualityOCICleanupTimeout)
	defer cancel()
	_, err := runQualityRuntimeCommand(ctx, runtimePath, runtimeName, tempDir, []string{"rm", "--force", containerID}, 4096)
	return err
}

func stopAndRemoveQualityContainer(runtimePath, runtimeName, tempDir, containerID string) error {
	_ = stopQualityContainer(runtimePath, runtimeName, tempDir, containerID)
	return removeQualityContainer(runtimePath, runtimeName, tempDir, containerID)
}

func parseGoTestCounts(output string) (total, failed int) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- PASS:") || strings.HasPrefix(line, "--- FAIL:") || strings.HasPrefix(line, "--- SKIP:") {
			total++
			if strings.HasPrefix(line, "--- FAIL:") {
				failed++
			}
		}
	}
	return total, failed
}

func daemonQualityDigest(result QualityGateResult) string {
	return qualityResultDigest(result)
}

func qualityResultDigest(result QualityGateResult) string {
	var exitCode *int
	if result.ExitCode != nil {
		value := *result.ExitCode
		exitCode = &value
	}
	var image, imageID, snapshotDigest, sourceSnapshot, packageManagerVersion string
	var networkDisabled, hostEnvironmentInherited, taskWorktreeMounted bool
	var memoryLimit, cpuLimit, pidLimit, tmpfsLimit string
	if result.Evidence != nil {
		image, _ = result.Evidence["container_image"].(string)
		imageID, _ = result.Evidence["container_image_id"].(string)
		snapshotDigest, _ = result.Evidence["source_snapshot_digest"].(string)
		sourceSnapshot, _ = result.Evidence["source_snapshot"].(string)
		packageManagerVersion, _ = result.Evidence["package_manager_version"].(string)
		networkDisabled, _ = result.Evidence["network_disabled"].(bool)
		hostEnvironmentInherited, _ = result.Evidence["host_environment_inherited"].(bool)
		taskWorktreeMounted, _ = result.Evidence["task_worktree_mounted"].(bool)
		memoryLimit, cpuLimit, pidLimit, tmpfsLimit = qualityResourceLimitValues(result.Evidence["resource_limits"])
	}
	data, _ := json.Marshal(struct {
		Passed                   bool   `json:"passed"`
		RunnerType               string `json:"runner_type"`
		TaskID                   string `json:"task_id"`
		GateType                 string `json:"gate_type"`
		Command                  string `json:"command"`
		ContainerImage           string `json:"container_image"`
		ContainerImageID         string `json:"container_image_id"`
		SnapshotDigest           string `json:"source_snapshot_digest"`
		SourceSnapshot           string `json:"source_snapshot"`
		PackageManagerVersion    string `json:"package_manager_version"`
		NetworkDisabled          bool   `json:"network_disabled"`
		HostEnvironmentInherited bool   `json:"host_environment_inherited"`
		TaskWorktreeMounted      bool   `json:"task_worktree_mounted"`
		MemoryLimit              string `json:"memory_limit"`
		CPULimit                 string `json:"cpu_limit"`
		PIDLimit                 string `json:"pid_limit"`
		TmpfsLimit               string `json:"tmpfs_limit"`
		ExitCode                 *int   `json:"exit_code"`
		WorktreePath             string `json:"worktree_path"`
		RepoPath                 string `json:"repo_path"`
		CommitSHA                string `json:"commit_sha"`
		DurationMs               int64  `json:"duration_ms"`
		Stdout                   string `json:"stdout"`
		Stderr                   string `json:"stderr"`
		OutputTruncated          bool   `json:"output_truncated"`
		TestCount                int    `json:"test_count"`
		FailedCount              int    `json:"failed_count"`
		Error                    string `json:"error"`
	}{result.Passed, result.RunnerType, result.TaskID, result.GateType, result.ExecutedCommand,
		image, imageID, snapshotDigest, sourceSnapshot, packageManagerVersion, networkDisabled, hostEnvironmentInherited,
		taskWorktreeMounted, memoryLimit, cpuLimit, pidLimit, tmpfsLimit, exitCode,
		result.WorktreePath, result.RepoPath, result.CommitSHA, result.DurationMs,
		result.Stdout, result.Stderr, result.OutputTruncated, result.TestCount,
		result.FailedCount, boundedQualityString(result.Error, 4096)})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func qualityResourceLimitValues(value any) (memory, cpus, pids, tmpfs string) {
	switch limits := value.(type) {
	case map[string]string:
		return limits["memory"], limits["cpus"], limits["pids"], limits["tmpfs"]
	case map[string]any:
		memory, _ = limits["memory"].(string)
		cpus, _ = limits["cpus"].(string)
		pids, _ = limits["pids"].(string)
		tmpfs, _ = limits["tmpfs"].(string)
	}
	return memory, cpus, pids, tmpfs
}

func boundedQualityString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
