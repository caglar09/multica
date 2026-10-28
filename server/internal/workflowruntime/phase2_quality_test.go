package workflowruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

type qualityGateRunnerFunc func(context.Context, projectorchestration.QualityGateRequest) (projectorchestration.QualityGateResult, error)

func (run qualityGateRunnerFunc) Run(ctx context.Context, request projectorchestration.QualityGateRequest) (projectorchestration.QualityGateResult, error) {
	return run(ctx, request)
}

func TestBrowserQAArtifactContentKeepsReviewableScreenshots(t *testing.T) {
	screenshots := []any{map[string]any{
		"path": "qa-0001.jpg", "content_type": "image/jpeg", "sha256": "digest", "data_base64": "aGVsbG8=",
	}}
	content, ok := browserQAArtifactContent("acceptance", "passed", "task-id", "commit-sha", "daemon_oci", map[string]any{
		"browser_qa": map[string]any{"screenshots": screenshots, "console_errors": []string{}},
	})
	if !ok {
		t.Fatal("browser QA evidence was not selected for artifact persistence")
	}
	if content["commit_sha"] != "commit-sha" || content["runner_type"] != "daemon_oci" {
		t.Fatalf("artifact content lost commit-bound runner provenance: %#v", content)
	}
	qa, ok := content["browser_qa"].(map[string]any)
	if !ok || len(qa["screenshots"].([]any)) != 1 {
		t.Fatalf("artifact content lost screenshot evidence: %#v", content)
	}
	if _, ok := browserQAArtifactContent("unit_test", "passed", "task-id", "commit-sha", "daemon_oci", map[string]any{
		"browser_qa": map[string]any{"screenshots": screenshots},
	}); ok {
		t.Fatal("non-acceptance gate wrote a browser QA artifact")
	}
}

func TestQualityGate(t *testing.T) {
	t.Run("NoRunnerIsUnavailable", func(t *testing.T) {
		_, status, err := evaluateQualityGate(context.Background(), nil, projectorchestration.QualityGateRequest{})
		if status != "unavailable" || !errors.Is(err, projectorchestration.ErrQualityGateUnavailable) {
			t.Fatalf("no-runner outcome = %q, %v; want unavailable", status, err)
		}
	})

	t.Run("RunnerClaimWithoutTaskBoundEvidenceIsUnavailable", func(t *testing.T) {
		code := 0
		request := projectorchestration.QualityGateRequest{
			WorkspaceID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
			ProjectID:   pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			PlanID:      pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
			NodeID:      pgtype.UUID{Bytes: [16]byte{4}, Valid: true},
			IssueID:     pgtype.UUID{Bytes: [16]byte{5}, Valid: true},
			TaskID:      pgtype.UUID{Bytes: [16]byte{6}, Valid: true},
			GateType:    "unit_test", CommitSHA: "0123456789012345678901234567890123456789",
			WorktreePath: "/recorded/worktree", RepoPath: "/recorded/repo",
		}
		runner := qualityGateRunnerFunc(func(context.Context, projectorchestration.QualityGateRequest) (projectorchestration.QualityGateResult, error) {
			return projectorchestration.QualityGateResult{Passed: true, ExitCode: &code}, nil
		})
		result, status, err := evaluateQualityGate(context.Background(), runner, request)
		if status != "unavailable" || err == nil || result.Passed {
			t.Fatalf("unattributed pass result = %+v, status=%q, error=%v", result, status, err)
		}
	})

	t.Run("AgentSelfReportCannotPass", func(t *testing.T) {
		artifact := map[string]any{
			"tests":   []any{map[string]any{"status": "passed"}},
			"context": "keep this context",
			"result": map[string]any{
				"tests":   []any{map[string]any{"status": "passed"}},
				"summary": "agent supplied summary",
			},
		}
		request := projectorchestration.QualityGateRequest{Artifact: withoutAgentTestClaims(artifact)}
		called := false
		runner := qualityGateRunnerFunc(func(_ context.Context, got projectorchestration.QualityGateRequest) (projectorchestration.QualityGateResult, error) {
			called = true
			if _, exists := got.Artifact["tests"]; exists {
				t.Error("top-level agent test claim reached deterministic runner")
			}
			result, _ := got.Artifact["result"].(map[string]any)
			if _, exists := result["tests"]; exists {
				t.Error("nested agent test claim reached deterministic runner")
			}
			if got.Artifact["context"] != "keep this context" || result["summary"] != "agent supplied summary" {
				t.Error("sanitizing the agent claim removed unrelated artifact context")
			}
			return projectorchestration.QualityGateResult{Passed: false, Error: "runner did not verify tests"}, nil
		})
		result, status, err := evaluateQualityGate(context.Background(), runner, request)
		if !called || status != "failed" || err == nil || result.Passed {
			t.Fatalf("agent claim outcome = passed:%v, status:%q, err:%v; want failed runner outcome", result.Passed, status, err)
		}
		if _, exists := artifact["tests"]; !exists {
			t.Fatal("sanitizing runner input mutated the original task artifact")
		}
	})

	t.Run("NonBlockingFailureIsFailed", func(t *testing.T) {
		artifact := map[string]any{"result": map[string]any{
			"tests": []any{map[string]any{
				"name": "unit", "status": "failed", "evidence": "outside this change",
			}},
			"findings": []any{map[string]any{
				"category": "test", "blocking": false, "evidence": "outside this change",
			}},
		}}
		runner := qualityGateRunnerFunc(func(_ context.Context, request projectorchestration.QualityGateRequest) (projectorchestration.QualityGateResult, error) {
			result, _ := request.Artifact["result"].(map[string]any)
			if _, exists := result["tests"]; exists {
				t.Error("agent test status reached deterministic runner")
			}
			if _, exists := result["findings"]; !exists {
				t.Error("sanitizing agent tests also removed unrelated finding context")
			}
			return projectorchestration.QualityGateResult{Passed: false, Error: "failure outside this change"}, nil
		})
		_, status, err := evaluateQualityGate(context.Background(), runner, projectorchestration.QualityGateRequest{
			Artifact: withoutAgentTestClaims(artifact),
		})
		if status != "failed" || err == nil {
			t.Fatalf("non-blocking runner failure outcome = %q, %v; want failed", status, err)
		}
	})
}

func TestQualityEvidenceMustBelongToCurrentTask(t *testing.T) {
	requirements := []projectorchestration.QualityRequirement{
		{GateType: "review", Deterministic: false},
		{GateType: "unit_test", Deterministic: true},
		{GateType: "integration_test", Deterministic: true},
	}
	currentEvidence := map[string]taskQualityEvidence{
		"unit_test":        {Status: "passed", SourceTaskID: "task-current"},
		"integration_test": {Status: "passed", SourceTaskID: "task-current"},
	}
	if !hasCurrentDeterministicQualityEvidence(requirements, currentEvidence, "task-current") {
		t.Fatal("current task deterministic evidence should qualify")
	}
	currentEvidence["integration_test"] = taskQualityEvidence{Status: "passed", SourceTaskID: "task-previous"}
	if hasCurrentDeterministicQualityEvidence(requirements, currentEvidence, "task-current") {
		t.Fatal("a previous task's passing evidence must not qualify a new commit")
	}
	delete(currentEvidence, "integration_test")
	if hasCurrentDeterministicQualityEvidence(requirements, currentEvidence, "task-current") {
		t.Fatal("missing deterministic gate evidence must not qualify a change set")
	}
	if hasCurrentDeterministicQualityEvidence(nil, currentEvidence, "task-current") {
		t.Fatal("a project with no deterministic gate must not qualify a change set")
	}
}

func TestStructuredQATestClaimClassification(t *testing.T) {
	passed := map[string]any{"result": map[string]any{"tests": []any{
		map[string]any{"name": "unit", "status": "passed"},
		map[string]any{"name": "integration", "status": "PASSED"},
	}}}
	if count, ok := structuredTestsPassed("unit_test", passed); !ok || count != 2 {
		t.Fatalf("structuredTestsPassed() = %d, %v; want 2, true", count, ok)
	}

	failed := map[string]any{"result": map[string]any{"tests": []any{
		map[string]any{"name": "unit", "status": "failed"},
	}}}
	if _, ok := structuredTestsPassed("unit_test", failed); ok {
		t.Fatal("failed test claim must not be classified as passing")
	}
	nonBlockingFailure := map[string]any{"result": map[string]any{
		"tests":    []any{map[string]any{"name": "baseline", "status": "failed", "evidence": "known upstream failure"}},
		"findings": []any{map[string]any{"blocking": false, "evidence": "known upstream failure"}},
	}}
	if count, ok := structuredTestsPassed("unit_test", nonBlockingFailure); !ok || count != 1 {
		t.Fatalf("explicit non-blocking failure = %d, %v; want 1, true", count, ok)
	}
	legacyMigrationFailure := map[string]any{"result": map[string]any{
		"tests":    []any{map[string]any{"name": "migration numeric-prefix lint", "status": "failed", "evidence": "duplicate 468_comment_deleted_at"}},
		"findings": []any{map[string]any{"blocking": false, "category": "migration", "description": "upstream migration prefix 468 is duplicated"}},
	}}
	if _, ok := structuredTestsPassed("unit_test", legacyMigrationFailure); !ok {
		t.Fatal("legacy non-blocking migration evidence must pass")
	}
	nonBlockingSkip := map[string]any{"result": map[string]any{
		"tests":    []any{map[string]any{"name": "full Go suite", "status": "skipped", "evidence": "unrelated process hung"}},
		"findings": []any{map[string]any{"blocking": false, "category": "test", "description": "The broad Go test suite did not complete."}},
	}}
	if _, ok := structuredTestsPassed("unit_test", nonBlockingSkip); !ok {
		t.Fatal("explicit non-blocking skipped test must pass")
	}
	nonBlockingOutsideChange := map[string]any{"result": map[string]any{
		"tests": []any{map[string]any{"name": "package race test", "status": "failed", "evidence": "Existing race outside this change."}},
	}}
	if _, ok := structuredTestsPassed("unit_test", nonBlockingOutsideChange); !ok {
		t.Fatal("explicit outside-change test failure must pass")
	}
	if _, ok := structuredTestsPassed("unit_test", map[string]any{}); ok {
		t.Fatal("missing test evidence must not pass a deterministic gate")
	}
	if _, ok := structuredTestsPassed("security", passed); ok {
		t.Fatal("structured task claims must not replace a security runner")
	}
}

func TestStructuredQAHandoff(t *testing.T) {
	valid := []byte(`{"verdict":"pass","status":"passed","tests":[{"name":"pytest","status":"passed","evidence":"317 passed"},{"name":"live scan","status":"not_run","evidence":"policy restriction"}],"findings":[{"category":"test","blocking":false,"evidence":"policy restriction"}]}`)
	if !structuredQAHandoff(valid) {
		t.Fatal("valid QA handoff with explicitly non-blocking not-run test must pass")
	}

	mismatchedFinding := []byte(`{"verdict":"pass","status":"passed","tests":[{"name":"live scan","status":"not_run","evidence":"policy restriction"}],"findings":[{"category":"test","blocking":false,"evidence":"different evidence"}]}`)
	if structuredQAHandoff(mismatchedFinding) {
		t.Fatal("not-run test without matching non-blocking evidence must not pass")
	}

	failed := []byte(`{"verdict":"fail","status":"passed","tests":[{"name":"pytest","status":"passed","evidence":"317 passed"}]}`)
	if structuredQAHandoff(failed) {
		t.Fatal("failed verdict must not pass QA")
	}

	validArtifact := map[string]any{
		"valid":       true,
		"artifact_id": "123e4567-e89b-12d3-a456-426614174000",
		"agent_id":    "123e4567-e89b-12d3-a456-426614174001",
		"result": map[string]any{
			"verdict": "pass", "status": "passed",
			"tests": []any{map[string]any{"name": "acceptance", "status": "passed", "evidence": "all checks passed"}},
		},
	}
	if _, ok := validatedQAHandoffArtifactID("qa_handoff", validArtifact); !ok {
		t.Fatal("validated durable qa_handoff artifact must pass")
	}
	if _, ok := validatedQAHandoffArtifactID("qa_report", validArtifact); ok {
		t.Fatal("legacy qa_report must not satisfy formal qa_handoff")
	}
	if _, ok := validatedQAHandoffArtifactID("qa_handoff", map[string]any{
		"result": validArtifact["result"],
	}); ok {
		t.Fatal("passing comment content without persisted artifact identity must not pass")
	}

	original := []byte(`{"status":"completed"}`)
	comment := []byte(`{"verdict":"pass","status":"passed","tests":[{"name":"acceptance","status":"passed","evidence":"all checks passed"}]}`)
	if got := normalizeTaskResultFromAgentCommentContent(original, projectorchestration.NodeQA, comment); string(got) != string(original) {
		t.Fatalf("QA comment replaced the task result: got %s, want %s", got, original)
	}
}

func TestReviewApproved(t *testing.T) {
	if !reviewApproved(map[string]any{"result": map[string]any{"verdict": "approved"}}) {
		t.Fatal("approved verdict was not recognized")
	}
	if !reviewApproved(map[string]any{"result": map[string]any{"verdict": "approved", "goal_alignment_score": 80}}) {
		t.Fatal("approved verdict at the goal-alignment threshold was rejected")
	}
	if reviewApproved(map[string]any{"result": map[string]any{"verdict": "approved", "goal_alignment_score": 79}}) {
		t.Fatal("approved verdict below the goal-alignment threshold was accepted")
	}
	if reviewApproved(map[string]any{"result": map[string]any{"verdict": "changes_requested"}}) {
		t.Fatal("changes_requested verdict must not pass review")
	}
}

func TestQualityCommitURLSanitizesAndLinksRemote(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		want   string
	}{
		{
			name:   "ssh remote",
			remote: "git@github.com:multica-ai/multica.git",
			want:   "https://github.com/multica-ai/multica/commit/abc123",
		},
		{
			name:   "credentialed https remote",
			remote: "https://user:secret@example.com/org/repo.git",
			want:   "https://example.com/org/repo/commit/abc123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualityCommitURLFromRemote(tt.remote, "abc123"); got != tt.want {
				t.Fatalf("qualityCommitURLFromRemote() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := qualityCommitURLFromRemote("file:///tmp/repo", "abc123"); got != "" {
		t.Fatalf("local remote should not produce a browser URL, got %q", got)
	}
}
