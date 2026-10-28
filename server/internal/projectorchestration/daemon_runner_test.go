package projectorchestration

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestDaemonQualityGateRunner(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required to create the recorded task worktree")
	}

	t.Run("successful result is bound to the exact committed snapshot", func(t *testing.T) {
		request := newDaemonQualityRequest(t, "package runner\n")
		runtimePath := fakeQualityRuntime(t, fakeQualityRuntimeConfig{
			exitCode: 0,
			stdout:   "--- PASS: TestGate (0.00s)\nrunner output\n",
		})
		t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-reach-runtime")
		runner := &DaemonQualityGateRunner{runtimePath: runtimePath}
		result, err := runner.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Passed || result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("successful test result = %+v", result)
		}
		if result.CommitSHA != request.CommitSHA || result.RunnerType != "daemon_oci" ||
			result.ExecutedCommand != "go test -v ./..." || result.TestCount != 1 {
			t.Fatalf("quality evidence is not bound to the task commit: %+v", result)
		}
		if !strings.Contains(result.Stdout, "runner output") || len(result.ArtifactDigest) != 64 {
			t.Fatalf("missing bounded execution evidence: %+v", result)
		}
		for _, key := range []string{"container_image", "container_image_id", "source_snapshot_digest", "network_disabled", "resource_limits"} {
			if _, ok := result.Evidence[key]; !ok {
				t.Errorf("evidence is missing %q: %+v", key, result.Evidence)
			}
		}
		if result.Evidence["network_disabled"] != true || result.Evidence["host_environment_inherited"] != false {
			t.Fatalf("container isolation evidence is incomplete: %+v", result.Evidence)
		}
		if env, err := os.ReadFile(runtimeEnvironmentLog(runtimePath)); err == nil && strings.Contains(string(env), "AWS_SECRET_ACCESS_KEY") {
			t.Fatal("host secret environment variable was inherited by the OCI runtime")
		}
		args, err := os.ReadFile(runtimeArgsLog(runtimePath))
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"--network=none", "--read-only", "--pull=never", "--cap-drop=ALL", "--security-opt=no-new-privileges"} {
			if !strings.Contains(string(args), required) {
				t.Errorf("container invocation is missing %q: %s", required, args)
			}
		}
		if strings.Contains(string(args), request.WorktreePath) {
			t.Fatalf("OCI command mounted the task worktree instead of the commit snapshot: %s", args)
		}
	})

	t.Run("failing command cannot pass", func(t *testing.T) {
		request := newDaemonQualityRequest(t, "package runner\n")
		runtimePath := fakeQualityRuntime(t, fakeQualityRuntimeConfig{
			exitCode: 1,
			stdout:   "--- FAIL: TestGate (0.00s)\nintentional failure\n",
		})
		result, err := (&DaemonQualityGateRunner{runtimePath: runtimePath}).Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.Passed || result.ExitCode == nil || *result.ExitCode == 0 || !strings.Contains(result.Stdout, "intentional failure") {
			t.Fatalf("failing test result = %+v", result)
		}
	})

	t.Run("timeout stops the OCI container and cannot pass", func(t *testing.T) {
		request := newDaemonQualityRequest(t, "package runner\n")
		runtimePath := fakeQualityRuntime(t, fakeQualityRuntimeConfig{
			exitCode:    137,
			timeoutWait: true,
		})
		runner := &DaemonQualityGateRunner{timeout: 100 * time.Millisecond, runtimePath: runtimePath}
		result, err := runner.Run(context.Background(), request)
		if err == nil || result.Passed || !strings.Contains(result.Error, "exceeded") {
			t.Fatalf("timed-out test result = %+v, error = %v", result, err)
		}
	})

	t.Run("stale commit is rejected before container start", func(t *testing.T) {
		request := newDaemonQualityRequest(t, "package runner\n")
		request.CommitSHA = strings.Repeat("0", 40)
		runtimePath := fakeQualityRuntime(t, fakeQualityRuntimeConfig{exitCode: 0})
		result, err := (&DaemonQualityGateRunner{runtimePath: runtimePath}).Run(context.Background(), request)
		if err == nil || result.Passed {
			t.Fatalf("stale commit result = %+v, error = %v", result, err)
		}
		if _, statErr := os.Stat(runtimeArgsLog(runtimePath)); !os.IsNotExist(statErr) {
			t.Fatalf("stale commit unexpectedly invoked OCI runtime; stat error=%v", statErr)
		}
	})

	t.Run("dirty worktree is rejected before container start", func(t *testing.T) {
		request := newDaemonQualityRequest(t, "package runner\n")
		if err := os.WriteFile(filepath.Join(request.WorktreePath, "untracked.txt"), []byte("dirty"), 0o600); err != nil {
			t.Fatal(err)
		}
		runtimePath := fakeQualityRuntime(t, fakeQualityRuntimeConfig{exitCode: 0})
		result, err := (&DaemonQualityGateRunner{runtimePath: runtimePath}).Run(context.Background(), request)
		if err == nil || result.Passed {
			t.Fatalf("dirty worktree result = %+v, error = %v", result, err)
		}
		if _, statErr := os.Stat(runtimeArgsLog(runtimePath)); !os.IsNotExist(statErr) {
			t.Fatalf("dirty worktree unexpectedly invoked OCI runtime; stat error=%v", statErr)
		}
	})

	t.Run("malformed output evidence cannot pass", func(t *testing.T) {
		request := newDaemonQualityRequest(t, "package runner\n")
		runtimePath := fakeQualityRuntime(t, fakeQualityRuntimeConfig{exitCode: 0, malformedLogs: true})
		result, err := (&DaemonQualityGateRunner{runtimePath: runtimePath}).Run(context.Background(), request)
		if err == nil || result.Passed || !strings.Contains(result.Error, "malformed") {
			t.Fatalf("malformed evidence result = %+v, error = %v", result, err)
		}
	})

	t.Run("unsupported Cargo image fails closed", func(t *testing.T) {
		request := newDaemonQualityRequest(t, "package runner\n")
		if err := os.WriteFile(filepath.Join(request.WorktreePath, "Cargo.toml"), []byte("[package]\nname='runner'\nversion='0.1.0'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(request.WorktreePath, "go.mod")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(request.WorktreePath, "runner_test.go")); err != nil {
			t.Fatal(err)
		}
		gitTestCommand(t, request.WorktreePath, "add", "-A")
		gitTestCommand(t, request.WorktreePath, "commit", "-m", "unsupported Cargo project")
		request.CommitSHA = gitTestCommand(t, request.WorktreePath, "rev-parse", "HEAD")
		result, err := (&DaemonQualityGateRunner{runtimePath: fakeQualityRuntime(t, fakeQualityRuntimeConfig{exitCode: 0})}).Run(context.Background(), request)
		if err == nil || result.Passed || !strings.Contains(result.Error, "Cargo") {
			t.Fatalf("unsupported Cargo result = %+v, error = %v", result, err)
		}
	})
}

func TestDetectDaemonQualityCommandUsesRecordedCommitScope(t *testing.T) {
	repo := t.TempDir()
	gitTestCommand(t, repo, "init")
	gitTestCommand(t, repo, "config", "user.name", "Quality Runner Test")
	gitTestCommand(t, repo, "config", "user.email", "quality-runner@example.invalid")
	for path, content := range map[string]string{
		"package.json":     `{"name":"monorepo"}`,
		"server/go.mod":    "module runner.test\n\ngo 1.26.9\n",
		"server/runner.go": "package runner\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitTestCommand(t, repo, "add", "-A")
	gitTestCommand(t, repo, "commit", "-m", "monorepo baseline")
	if err := os.WriteFile(filepath.Join(repo, "server/runner.go"), []byte("package runner\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "add", "server/runner.go")
	gitTestCommand(t, repo, "commit", "-m", "Go task")
	goCommit := gitTestCommand(t, repo, "rev-parse", "HEAD")
	command, err := detectDaemonQualityCommand(repo, "unit_test", goCommit)
	if err != nil || command.display != "go -C server test -v ./..." {
		t.Fatalf("Go monorepo command = %+v, error = %v", command, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("Go test scope remains unchanged.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "server/runner.go"), []byte("package runner\n// docs-only companion change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "add", "README.md", "server/runner.go")
	gitTestCommand(t, repo, "commit", "-m", "Go change with docs")
	docsCommit := gitTestCommand(t, repo, "rev-parse", "HEAD")
	command, err = detectDaemonQualityCommand(repo, "unit_test", docsCommit)
	if err != nil || command.display != "go -C server test -v ./..." {
		t.Fatalf("Go with documentation command = %+v, error = %v", command, err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "packages/core"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "packages/core/index.ts"), []byte("export {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "add", "packages/core/index.ts")
	gitTestCommand(t, repo, "commit", "-m", "Node task")
	nodeCommit := gitTestCommand(t, repo, "rev-parse", "HEAD")
	command, err = detectDaemonQualityCommand(repo, "unit_test", nodeCommit)
	if err != nil || command.display != "corepack pnpm test" {
		t.Fatalf("Node monorepo command = %+v, error = %v", command, err)
	}
	if err := os.WriteFile(filepath.Join(repo, "server/runner.go"), []byte("package runner\n// mixed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "packages/core/index.ts"), []byte("export const mixed = true;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "add", "server/runner.go", "packages/core/index.ts")
	gitTestCommand(t, repo, "commit", "-m", "mixed task")
	mixedCommit := gitTestCommand(t, repo, "rev-parse", "HEAD")
	if _, err := detectDaemonQualityCommand(repo, "unit_test", mixedCommit); err == nil {
		t.Fatal("mixed-language commit selected a single project command")
	}
}

// TestDaemonQualityGateRunnerOCIIntegration is an opt-in smoke test that uses
// the actual local OCI engine. It never pulls an image and intentionally
// records Node/repository outcomes because those commands may lack offline
// dependencies in the pinned image.
func TestDaemonQualityGateRunnerOCIIntegration(t *testing.T) {
	if os.Getenv("MULTICA_RUN_OCI_QUALITY_SMOKE") != "1" {
		t.Skip("set MULTICA_RUN_OCI_QUALITY_SMOKE=1 to run pinned-image OCI smoke checks")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required to create committed smoke-test worktrees")
	}
	runner := NewDaemonQualityGateRunner()
	request := newDaemonQualityRequest(t, "package runner\nimport \"testing\"\nfunc TestSmoke(t *testing.T) {}\n")
	result, err := runner.Run(context.Background(), request)
	if err != nil || !result.Passed || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("real Go OCI smoke failed: result=%+v error=%v", result, err)
	}
	if err := ValidateQualityGatePass(request, result); err != nil {
		t.Fatalf("real Go OCI evidence failed validation: %v", err)
	}
	t.Logf("Go OCI smoke: command=%q image=%v image_id=%v snapshot_digest=%v exit=%d result_digest=%s",
		result.ExecutedCommand, result.Evidence["container_image"], result.Evidence["container_image_id"],
		result.Evidence["source_snapshot_digest"], *result.ExitCode, result.ArtifactDigest)

	configureNodeQualityRequest(t, &request)
	nodeResult, nodeErr := runner.Run(context.Background(), request)
	logOCIQualityAttempt(t, "Node fixture", nodeResult, nodeErr)
	if nodeErr == nil && nodeResult.Passed {
		if err := ValidateQualityGatePass(request, nodeResult); err != nil {
			t.Fatalf("real Node OCI evidence failed validation: %v", err)
		}
	}

	rootOutput, rootErr := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if rootErr != nil {
		t.Fatalf("resolve repository root for offline command attempt: %v", rootErr)
	}
	repoRoot := strings.TrimSpace(string(rootOutput))
	repoWorktree := filepath.Join(t.TempDir(), "repository-worktree")
	gitTestCommand(t, repoRoot, "worktree", "add", "--detach", repoWorktree, "HEAD")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", repoWorktree).Run()
	})
	rootRequest := request
	rootRequest.RepoPath = repoRoot
	rootRequest.WorktreePath = repoWorktree
	rootRequest.CommitSHA = gitTestCommand(t, repoWorktree, "rev-parse", "HEAD")
	rootResult, rootRunErr := runner.Run(context.Background(), rootRequest)
	logOCIQualityAttempt(t, "repository pnpm test", rootResult, rootRunErr)
	if rootRunErr == nil && rootResult.Passed {
		if err := ValidateQualityGatePass(rootRequest, rootResult); err != nil {
			t.Fatalf("repository OCI evidence failed validation: %v", err)
		}
	}
}

func TestDaemonQualityGateRunnerMissingPNPMIsUnavailable(t *testing.T) {
	request := newDaemonQualityRequest(t, "package runner\n")
	configureNodeQualityRequest(t, &request)
	runtimePath := fakeQualityRuntime(t, fakeQualityRuntimeConfig{exitCode: 0})
	result, err := (&DaemonQualityGateRunner{runtimePath: runtimePath}).Run(context.Background(), request)
	if err == nil || result.Passed || !strings.Contains(result.Error, "quality gate unavailable") {
		t.Fatalf("missing pnpm preflight result=%+v error=%v", result, err)
	}
	args, err := os.ReadFile(runtimeArgsLog(runtimePath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--pull=never") || !strings.Contains(string(args), "--network=none") ||
		!strings.Contains(string(args), "corepack pnpm --version") || strings.Contains(string(args), "corepack pnpm test") {
		t.Fatalf("missing-package-manager preflight did not fail before the test command: %s", args)
	}
}

func configureNodeQualityRequest(t *testing.T, request *QualityGateRequest) {
	t.Helper()
	if err := os.Remove(filepath.Join(request.WorktreePath, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(request.WorktreePath, "runner_test.go")); err != nil {
		t.Fatal(err)
	}
	packageJSON := `{"name":"quality-runner-smoke","version":"1.0.0","packageManager":"pnpm@10.28.2","scripts":{"test":"node -p 1"}}` + "\n"
	if err := os.WriteFile(filepath.Join(request.WorktreePath, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, request.WorktreePath, "add", "-A")
	gitTestCommand(t, request.WorktreePath, "commit", "-m", "node quality fixture")
	request.CommitSHA = gitTestCommand(t, request.WorktreePath, "rev-parse", "HEAD")
}

func logOCIQualityAttempt(t *testing.T, name string, result QualityGateResult, runErr error) {
	t.Helper()
	if runErr != nil {
		t.Logf("%s OCI attempt was rejected: command=%q image=%v image_id=%v exit=%v digest=%s error=%q stdout=%q stderr=%q",
			name, result.ExecutedCommand, result.Evidence["container_image"], result.Evidence["container_image_id"],
			qualitySmokeExitCode(result.ExitCode), result.ArtifactDigest, runErr, result.Stdout, result.Stderr)
		return
	}
	t.Logf("%s OCI attempt: command=%q image=%v image_id=%v snapshot_digest=%v exit=%v passed=%v digest=%s stdout=%q stderr=%q error=%q",
		name, result.ExecutedCommand, result.Evidence["container_image"], result.Evidence["container_image_id"],
		result.Evidence["source_snapshot_digest"], qualitySmokeExitCode(result.ExitCode), result.Passed, result.ArtifactDigest,
		result.Stdout, result.Stderr, result.Error)
}

func qualitySmokeExitCode(exitCode *int) any {
	if exitCode == nil {
		return nil
	}
	return *exitCode
}

type fakeQualityRuntimeConfig struct {
	exitCode      int
	stdout        string
	stderr        string
	malformedLogs bool
	timeoutWait   bool
}

func fakeQualityRuntime(t *testing.T, config fakeQualityRuntimeConfig) string {
	t.Helper()
	root := t.TempDir()
	runtimePath := filepath.Join(root, "docker")
	stdoutData := []byte(config.stdout)
	stderrData := []byte(config.stderr)
	logs := fmt.Sprintf("__MC_RESULT__%d:%d:%d\n%s\n__MC_STDERR__\n%s\n__MC_DONE__\n",
		config.exitCode, len(stdoutData), len(stderrData), base64.StdEncoding.EncodeToString(stdoutData), base64.StdEncoding.EncodeToString(stderrData))
	if config.malformedLogs {
		logs = "malformed runtime output\n"
	}
	marker := filepath.Join(root, "wait-once")
	argsLog := filepath.Join(root, "runtime-args.log")
	envLog := filepath.Join(root, "runtime-env.log")
	script := "#!/bin/sh\nset -eu\n"
	script += "env > " + shellTestQuote(envLog) + "\n"
	script += "case \"$1\" in\n"
	script += "  image) printf '%s\\n' 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' ;;\n"
	script += "  run) printf '%s\\n' \"$*\" > " + shellTestQuote(argsLog) + "; shift; while [ \"$#\" -gt 0 ]; do if [ \"$1\" = '--cidfile' ]; then printf '%s\\n' 'abcdef123456' > \"$2\"; shift 2; else shift; fi; done ;;\n"
	script += "  wait)\n"
	if config.timeoutWait {
		script += "    if [ ! -e " + shellTestQuote(marker) + " ]; then touch " + shellTestQuote(marker) + "; exec sleep 60; fi\n"
	}
	script += fmt.Sprintf("    printf '%%s\\n' '%d' ;;\n", config.exitCode)
	script += "  logs) printf '%s' " + shellTestQuote(logs) + " ;;\n"
	script += "  stop|rm) exit 0 ;;\n"
	script += "  *) exit 90 ;;\n"
	script += "esac\n"
	if err := os.WriteFile(runtimePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return runtimePath
}

func runtimeArgsLog(runtimePath string) string {
	return filepath.Join(filepath.Dir(runtimePath), "runtime-args.log")
}

func runtimeEnvironmentLog(runtimePath string) string {
	return filepath.Join(filepath.Dir(runtimePath), "runtime-env.log")
}

func shellTestQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func newDaemonQualityRequest(t *testing.T, testSource string) QualityGateRequest {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "task-worktree")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "init")
	gitTestCommand(t, repo, "config", "user.name", "Quality Runner Test")
	gitTestCommand(t, repo, "config", "user.email", "quality-runner@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module runner.test\n\ngo 1.26.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "runner_test.go"), []byte(testSource), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, repo, "add", "go.mod", "runner_test.go")
	gitTestCommand(t, repo, "commit", "-m", "task commit")
	gitTestCommand(t, repo, "worktree", "add", "-b", "task-quality", worktree, "HEAD")
	commitSHA := gitTestCommand(t, worktree, "rev-parse", "HEAD")
	return QualityGateRequest{
		WorkspaceID:  pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		ProjectID:    pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		PlanID:       pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
		NodeID:       pgtype.UUID{Bytes: [16]byte{4}, Valid: true},
		IssueID:      pgtype.UUID{Bytes: [16]byte{5}, Valid: true},
		TaskID:       pgtype.UUID{Bytes: [16]byte{6}, Valid: true},
		GateType:     "unit_test",
		WorktreePath: worktree,
		RepoPath:     repo,
		CommitSHA:    commitSHA,
	}
}

func gitTestCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	gitArgs := append([]string{"-C", dir}, args...)
	output, err := exec.Command("git", gitArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
