package projectorchestration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestConflictResolutionResultRequiresCompleteOutcome(t *testing.T) {
	if err := ValidateConflictResolutionResult(ConflictResolutionResult{
		Resolved: true, ResolutionSHA: "0123456789012345678901234567890123456789", Explanation: "combined both changes",
	}); err != nil {
		t.Fatalf("valid resolution result rejected: %v", err)
	}
	if err := ValidateConflictResolutionResult(ConflictResolutionResult{Resolved: true, Explanation: "missing SHA"}); err == nil {
		t.Fatal("resolved result without a commit SHA was accepted")
	}
	if err := ValidateConflictResolutionResult(ConflictResolutionResult{Explanation: "failed without escalation"}); err == nil {
		t.Fatal("unresolved result without a visible escalation was accepted")
	}
}

func TestValidateConflictResolutionCommitRequiresBothConflictParents(t *testing.T) {
	repo, baseSHA := newMergeQueueTestRepo(t)
	mergeTestGit(t, repo, "switch", "-c", "task/source")
	writeMergeTestFile(t, repo, "README", "source change\n")
	mergeTestGit(t, repo, "commit", "-am", "source change")
	sourceSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	mergeTestGit(t, repo, "switch", "main")
	writeMergeTestFile(t, repo, "README", "integration change\n")
	mergeTestGit(t, repo, "commit", "-am", "integration change")
	baseForMerge := mergeTestGit(t, repo, "rev-parse", "HEAD")

	parent := t.TempDir()
	worktree := filepath.Join(parent, "tree")
	mergeTestGit(t, repo, "worktree", "add", "--detach", worktree, baseForMerge)
	if _, err := mergeCommitIntoWorktree(context.Background(), worktree, parent, sourceSHA); err == nil {
		t.Fatal("expected the test branches to conflict")
	}
	conflictDiff := mergeTestGit(t, worktree, "diff", "--cc", "--no-ext-diff", "--no-color", "--")
	for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
		if !strings.Contains(conflictDiff, marker) {
			t.Fatalf("combined conflict diff is missing marker %q: %s", marker, conflictDiff)
		}
	}
	writeMergeTestFile(t, worktree, "README", "resolved both requirements\n")
	mergeTestGit(t, worktree, "add", "README")
	mergeTestGit(t, worktree, "commit", "-m", "resolve merge conflict")
	resolutionSHA := mergeTestGit(t, worktree, "rev-parse", "HEAD")

	if err := ValidateConflictResolutionCommit(context.Background(), repo, baseForMerge, sourceSHA, resolutionSHA); err != nil {
		t.Fatalf("valid conflict resolution commit rejected: %v", err)
	}
	mergeTestGit(t, repo, "switch", "-c", "task/unrelated", baseSHA)
	writeMergeTestFile(t, repo, "unrelated.txt", "not part of the resolution\n")
	mergeTestGit(t, repo, "add", "unrelated.txt")
	mergeTestGit(t, repo, "commit", "-m", "unrelated change")
	unrelatedSHA := mergeTestGit(t, repo, "rev-parse", "HEAD")
	if err := ValidateConflictResolutionCommit(context.Background(), repo, baseForMerge, sourceSHA, unrelatedSHA); err == nil {
		t.Fatal("resolution with a missing source parent was accepted")
	}
	if got := mergeTestGit(t, repo, "rev-parse", "refs/heads/main"); got != baseForMerge {
		t.Fatalf("integration branch moved during proposal creation: got %s, want %s", got, baseForMerge)
	}
	mergeTestGit(t, repo, "worktree", "remove", "--force", worktree)
}
