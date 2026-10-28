package projectorchestration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/testutil"
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
	candidateTreeSHA := mergeTestGit(t, worktree, "rev-parse", candidateSHA+"^{tree}")

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
	if _, err := queue.verifyCandidate(context.Background(), claim, repo, worktree, candidateSHA, candidateTreeSHA, "integration_test"); err == nil {
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

func TestMergeQueue_PersistsCandidateQualityGateIDsAndTree(t *testing.T) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	base := testutil.New(pool, "", "")
	userID := base.User(t, "Wave 3 merged candidate quality test", "wave3-"+uuid.NewString()+"@example.com")
	workspaceID := base.Workspace(t, "Wave 3 merged candidate quality test", "wave3-"+uuid.NewString())
	base.Member(t, workspaceID, userID, "owner")
	registerWave3WorkspaceCleanup(t, pool, workspaceID)
	fixtureData := testutil.New(pool, workspaceID, userID)
	projectID := fixtureData.Project(t, "Wave 3 merged candidate quality test")
	issueID := fixtureData.Issue(t, "Merged candidate quality issue", testutil.Cols{"status": "todo"})
	workspaceUUID := wave3UUID(t, workspaceID)
	projectUUID := wave3UUID(t, projectID)
	issueUUID := wave3UUID(t, issueID)
	var planID, nodeID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_plan (
			workspace_id, project_id, revision, source_revision, planner_name, goal,
			plan_digest, status
		) VALUES ($1,$2,1,$3,'test','Merged candidate quality evidence', $4, 'awaiting_approval')
		RETURNING id
	`, workspaceUUID, projectUUID, "candidate-quality-"+uuid.NewString(), strings.Repeat("a", 64)).Scan(&planID); err != nil {
		t.Fatalf("insert candidate quality plan fixture: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_plan_node (
			plan_id, workspace_id, project_id, node_key, kind, title, materialized_issue_id, status
		) VALUES ($1,$2,$3,'implementation','implementation','Implementation',$4,'ready')
		RETURNING id
	`, planID, workspaceUUID, projectUUID, issueUUID).Scan(&nodeID); err != nil {
		t.Fatalf("insert candidate quality plan node fixture: %v", err)
	}
	store := NewStore(pool)
	repo, candidateSHA := newMergeQueueTestRepo(t)
	treeSHA := mergeTestGit(t, repo, "rev-parse", candidateSHA+"^{tree}")
	claim := MergeQueueClaim{MergeReviewCandidate: MergeReviewCandidate{
		ChangeSetID: pgtype.UUID{Bytes: [16]byte{8}, Valid: true},
		WorkspaceID: workspaceUUID, ProjectID: projectUUID,
		PlanID: planID, NodeID: nodeID,
		IssueID: issueUUID, SourceTaskID: pgtype.UUID{Bytes: [16]byte{9}, Valid: true},
	}}
	qualityRunIDs := make([]string, 0, 2)
	for _, gateType := range []string{"build", "integration_test"} {
		gateEvidence := map[string]any{
			"gate_type": gateType, "passed": true, "executed_command": "go test ./...",
			"runner_type": "daemon_oci", "exit_code": 0,
			"worktree_path": repo, "artifact_digest": strings.Repeat("a", 64),
			"attestation": "test-attestation", "runner_evidence": map[string]any{"commit_sha": candidateSHA},
		}
		runID, err := store.persistMergedCandidateQualityRun(ctx, claim, candidateSHA, treeSHA, "merged_candidate", gateEvidence)
		if err != nil {
			t.Fatalf("persist %s merged-candidate quality run: %v", gateType, err)
		}
		qualityRunIDs = append(qualityRunIDs, runID)
		var storedCommit, storedTree, storedScope string
		if err := pool.QueryRow(ctx, `
			SELECT commit_sha, evidence->>'candidate_tree_sha', evidence->>'scope'
			FROM autonomous_project_quality_gate_run WHERE id=$1::uuid
		`, runID).Scan(&storedCommit, &storedTree, &storedScope); err != nil {
			t.Fatalf("reload persisted %s quality run: %v", gateType, err)
		}
		if storedCommit != candidateSHA || storedTree != treeSHA || storedScope != "merged_candidate" {
			t.Fatalf("persisted %s candidate = (%s, %s, %s), want (%s, %s, merged_candidate)", gateType, storedCommit, storedTree, storedScope, candidateSHA, treeSHA)
		}
	}
	mergeEvidence := map[string]any{
		"candidate_sha": candidateSHA, "candidate_tree_sha": treeSHA,
		"quality_gate_run_ids": qualityRunIDs,
	}
	if got := mergeEvidenceStrings(mergeEvidence, "quality_gate_run_ids"); len(got) != 2 || got[0] != qualityRunIDs[0] || got[1] != qualityRunIDs[1] {
		t.Fatalf("merge evidence quality gate IDs = %#v, want %#v", got, qualityRunIDs)
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
