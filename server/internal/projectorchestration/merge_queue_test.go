package projectorchestration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestMergeQueue_SequentialIntegration(t *testing.T) {
	repo, baseSHA := newMergeQueueTestRepo(t)

	mergeTestGit(t, repo, "switch", "-c", "task/one")
	writeMergeTestFile(t, repo, "one.txt", "one\n")
	mergeTestGit(t, repo, "add", "one.txt")
	mergeTestGit(t, repo, "commit", "-m", "task one")
	oneSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")

	mergeTestGit(t, repo, "switch", "main")
	mergeTestGit(t, repo, "switch", "-c", "task/two")
	writeMergeTestFile(t, repo, "two.txt", "two\n")
	mergeTestGit(t, repo, "add", "two.txt")
	mergeTestGit(t, repo, "commit", "-m", "task two")
	twoSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	mergeTestGit(t, repo, "switch", "main")
	mergeTestGit(t, repo, "branch", "integration", baseSHA)

	firstParent := t.TempDir()
	firstWorktree := filepath.Join(firstParent, "tree")
	mergeTestGit(t, repo, "worktree", "add", "--detach", firstWorktree, baseSHA)
	if _, err := mergeCommitIntoWorktree(context.Background(), firstWorktree, firstParent, oneSHA); err != nil {
		t.Fatalf("merge first task: %v", err)
	}
	firstMergeSHA := mergeTestGit(t, firstWorktree, "rev-parse", "HEAD")
	if err := compareAndSwapRef(context.Background(), repo, "refs/heads/integration", firstMergeSHA, baseSHA, false); err != nil {
		t.Fatalf("promote first integration commit: %v", err)
	}
	mergeTestGit(t, repo, "worktree", "remove", "--force", firstWorktree)

	secondParent := t.TempDir()
	secondWorktree := filepath.Join(secondParent, "tree")
	mergeTestGit(t, repo, "worktree", "add", "--detach", secondWorktree, firstMergeSHA)
	if _, err := mergeCommitIntoWorktree(context.Background(), secondWorktree, secondParent, twoSHA); err != nil {
		t.Fatalf("merge second task: %v", err)
	}
	secondMergeSHA := mergeTestGit(t, secondWorktree, "rev-parse", "HEAD")
	if err := compareAndSwapRef(context.Background(), repo, "refs/heads/integration", secondMergeSHA, firstMergeSHA, false); err != nil {
		t.Fatalf("promote second integration commit: %v", err)
	}
	if got := mergeTestGit(t, repo, "rev-parse", "refs/heads/integration"); got != secondMergeSHA {
		t.Fatalf("integration ref = %s, want %s", got, secondMergeSHA)
	}
	for name, want := range map[string]string{"one.txt": "one\n", "two.txt": "two\n"} {
		got, err := os.ReadFile(filepath.Join(secondWorktree, name))
		if err != nil || string(got) != want {
			t.Fatalf("merged %s = %q, err=%v; want %q", name, got, err, want)
		}
	}
	mergeTestGit(t, repo, "worktree", "remove", "--force", secondWorktree)
}

func TestMergeQueueRetryLimit(t *testing.T) {
	for _, attempt := range []int32{-1, 0, maxMergeAttempts - 1} {
		if mergeAttemptExhausted(attempt) {
			t.Fatalf("merge attempt %d unexpectedly exhausted the retry limit", attempt)
		}
	}
	for _, attempt := range []int32{maxMergeAttempts, maxMergeAttempts + 1} {
		if !mergeAttemptExhausted(attempt) {
			t.Fatalf("merge attempt %d did not exhaust the retry limit", attempt)
		}
	}
}

func TestMergeQueue_IntegrationTestFailure_DoesNotAdvanceRef(t *testing.T) {
	repo, baseSHA := newMergeQueueTestRepo(t)
	mergeTestGit(t, repo, "switch", "-c", "task/failed-gate")
	writeMergeTestFile(t, repo, "candidate.txt", "must stay out\n")
	mergeTestGit(t, repo, "add", "candidate.txt")
	mergeTestGit(t, repo, "commit", "-m", "candidate")
	sourceSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	mergeTestGit(t, repo, "switch", "main")
	mergeTestGit(t, repo, "branch", "integration", baseSHA)

	parent := t.TempDir()
	worktree := filepath.Join(parent, "tree")
	mergeTestGit(t, repo, "worktree", "add", "--detach", worktree, baseSHA)
	if _, err := mergeCommitIntoWorktree(context.Background(), worktree, parent, sourceSHA); err != nil {
		t.Fatalf("prepare candidate before failing integration gate: %v", err)
	}
	candidateSHA := mergeTestGit(t, worktree, "rev-parse", "HEAD")

	var gotRequest QualityGateRequest
	queue := NewMergeQueue(nil, mergeQueueRunnerFunc(func(_ context.Context, request QualityGateRequest) (QualityGateResult, error) {
		gotRequest = request
		return QualityGateResult{Passed: false, Error: "integration tests failed"}, nil
	}))
	claim := MergeQueueClaim{
		MergeReviewCandidate: MergeReviewCandidate{
			WorkspaceID:  pgtype.UUID{Valid: true, Bytes: [16]byte{1}},
			ProjectID:    pgtype.UUID{Valid: true, Bytes: [16]byte{2}},
			PlanID:       pgtype.UUID{Valid: true, Bytes: [16]byte{3}},
			NodeID:       pgtype.UUID{Valid: true, Bytes: [16]byte{4}},
			IssueID:      pgtype.UUID{Valid: true, Bytes: [16]byte{5}},
			SourceTaskID: pgtype.UUID{Valid: true, Bytes: [16]byte{6}},
		},
	}
	if _, err := queue.verifyCandidate(context.Background(), claim, repo, worktree, candidateSHA, "integration_test"); err == nil {
		t.Fatal("expected the failing Q2 integration gate to reject the candidate")
	}
	if gotRequest.CommitSHA != candidateSHA || gotRequest.GateType != "integration_test" || gotRequest.WorktreePath != worktree {
		t.Fatalf("Q2 request = %+v; want exact candidate SHA, worktree, and integration_test gate", gotRequest)
	}
	// A failed Q2 gate leaves the candidate only in its temporary worktree. The
	// production path never calls compareAndSwapRef unless both gates passed.
	if got := mergeTestGit(t, repo, "rev-parse", "refs/heads/integration"); got != baseSHA {
		t.Fatalf("integration ref moved to %s before verification; want %s", got, baseSHA)
	}
	if candidateSHA == baseSHA {
		t.Fatal("merge candidate unexpectedly equals the integration base")
	}
	mergeTestGit(t, repo, "worktree", "remove", "--force", worktree)
}

type mergeQueueRunnerFunc func(context.Context, QualityGateRequest) (QualityGateResult, error)

func (fn mergeQueueRunnerFunc) Run(ctx context.Context, request QualityGateRequest) (QualityGateResult, error) {
	return fn(ctx, request)
}

func TestMergeQueue_RefCASRejectsConcurrentAdvance(t *testing.T) {
	repo, baseSHA := newMergeQueueTestRepo(t)
	mergeTestGit(t, repo, "switch", "-c", "task/candidate")
	writeMergeTestFile(t, repo, "candidate.txt", "candidate\n")
	mergeTestGit(t, repo, "add", "candidate.txt")
	mergeTestGit(t, repo, "commit", "-m", "candidate")
	candidateSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	mergeTestGit(t, repo, "switch", "main")
	mergeTestGit(t, repo, "switch", "-c", "external/integration")
	writeMergeTestFile(t, repo, "external.txt", "external\n")
	mergeTestGit(t, repo, "add", "external.txt")
	mergeTestGit(t, repo, "commit", "-m", "external advance")
	externalSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	mergeTestGit(t, repo, "branch", "integration", externalSHA)

	err := compareAndSwapRef(context.Background(), repo, "refs/heads/integration", candidateSHA, baseSHA, false)
	if err == nil {
		t.Fatal("compare-and-swap advanced an integration ref that changed after capture")
	}
	if got := mergeTestGit(t, repo, "rev-parse", "refs/heads/integration"); got != externalSHA {
		t.Fatalf("integration ref = %s after rejected CAS, want %s", got, externalSHA)
	}
}

func TestMergeQueue_ExternalMergeDriversNeverExecute(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable unavailable")
	}
	repo := t.TempDir()
	mergeTestGit(t, repo, "init", "-q", "-b", "main")
	mergeTestGit(t, repo, "config", "user.name", "Merge Queue Test")
	mergeTestGit(t, repo, "config", "user.email", "merge-test@example.invalid")
	writeMergeTestFile(t, repo, "shared.txt", "base\n")
	writeMergeTestFile(t, repo, ".gitattributes", "shared.txt merge=malicious\n")
	mergeTestGit(t, repo, "add", "shared.txt", ".gitattributes")
	mergeTestGit(t, repo, "commit", "-m", "base")
	baseSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")

	mergeTestGit(t, repo, "switch", "-c", "task/change")
	writeMergeTestFile(t, repo, "shared.txt", "task change\n")
	mergeTestGit(t, repo, "commit", "-am", "task change")
	sourceSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	mergeTestGit(t, repo, "switch", "main")
	writeMergeTestFile(t, repo, "shared.txt", "integration change\n")
	mergeTestGit(t, repo, "commit", "-am", "integration change")
	integrationSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	ancestor, err := isAncestor(context.Background(), repo, baseSHA, integrationSHA)
	if err != nil || !ancestor {
		t.Fatalf("integration commit must descend from base: ancestor=%v err=%v", ancestor, err)
	}

	sentinel := filepath.Join(t.TempDir(), "merge-driver-ran")
	mergeTestGit(t, repo, "config", "merge.malicious.name", "test driver")
	mergeTestGit(t, repo, "config", "merge.malicious.driver", "touch "+sentinel)
	parent := t.TempDir()
	worktree := filepath.Join(parent, "tree")
	mergeTestGit(t, repo, "worktree", "add", "--detach", worktree, integrationSHA)
	if _, err := mergeCommitIntoWorktree(context.Background(), worktree, parent, sourceSHA); err == nil {
		t.Fatal("expected the conflicting custom merge driver to fail closed")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("repo-configured merge driver executed; sentinel stat err=%v", err)
	}
	if got := mergeTestGit(t, worktree, "diff", "--name-only", "--diff-filter=U"); got != "shared.txt" {
		t.Fatalf("unresolved merge paths = %q, want shared.txt", got)
	}
	mergeTestGit(t, repo, "worktree", "remove", "--force", worktree)
	if got := mergeTestGit(t, repo, "rev-parse", "main"); got != integrationSHA {
		t.Fatalf("temporary merge changed main to %s, want %s", got, integrationSHA)
	}
}

func TestMergeQueue_VerifiedGateEvidenceRequiresBothExactCommits(t *testing.T) {
	evidence := map[string]any{
		"source_task_id": "task-1",
		"gates": []any{
			map[string]any{"gate_type": "build", "task_id": "task-1", "commit_sha": "candidate", "passed": true},
			map[string]any{"gate_type": "integration_test", "task_id": "task-1", "commit_sha": "candidate", "passed": true},
		},
	}
	if !verifiedGateEvidenceMatches(evidence, "candidate") {
		t.Fatal("expected both exact-commit gates to be accepted")
	}
	wrongCommit := normalizeMergeEvidence(mustMergeEvidenceJSON(t, evidence))
	wrongCommit["gates"].([]any)[1].(map[string]any)["commit_sha"] = "old-source-commit"
	if verifiedGateEvidenceMatches(wrongCommit, "candidate") {
		t.Fatal("accepted integration evidence for a different commit")
	}
}

func TestMergeQueueTempPathIsScopedToChangeSet(t *testing.T) {
	id := pgtype.UUID{Valid: true, Bytes: [16]byte{1, 2, 3}}
	parent := filepath.Join(os.TempDir(), "multica-merge-00000000-0000-0000-0000-000000000000-attempt")
	worktree := filepath.Join(parent, "tree")
	if safeMergeWorktreePath(parent, worktree, id) {
		t.Fatal("accepted a temp path that does not match the encoded change-set UUID")
	}
}

func newMergeQueueTestRepo(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable unavailable")
	}
	repo := t.TempDir()
	mergeTestGit(t, repo, "init", "-q", "-b", "main")
	mergeTestGit(t, repo, "config", "user.name", "Merge Queue Test")
	mergeTestGit(t, repo, "config", "user.email", "merge-test@example.invalid")
	writeMergeTestFile(t, repo, "README", "base\n")
	mergeTestGit(t, repo, "add", "README")
	mergeTestGit(t, repo, "commit", "-m", "base")
	return repo, mergeTestGit(t, repo, "rev-parse", "HEAD")
}

func writeMergeTestFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mergeTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func mustMergeEvidenceJSON(t *testing.T, evidence map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestMergeQueueRequiresApprovedReviewResult(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{name: "approved", json: `{"verdict":"approved","summary":"Reviewed","findings":[]}`, want: true},
		{name: "changes requested", json: `{"verdict":"changes_requested","summary":"Fix required","findings":[{"blocking":true}]}`},
		{name: "approved with blocking finding", json: `{"verdict":"approved","summary":"Reviewed","findings":[{"blocking":true}]}`},
		{name: "missing summary", json: `{"verdict":"approved","findings":[]}`},
		{name: "malformed", json: `not-json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeReviewResultApproved([]byte(tt.json)); got != tt.want {
				t.Fatalf("mergeReviewResultApproved() = %v, want %v", got, tt.want)
			}
		})
	}
}
