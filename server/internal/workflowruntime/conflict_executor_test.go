package workflowruntime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

type conflictExecutorTestBackend struct {
	input           ConflictExecutionContext
	proposal        ConflictResolutionProposal
	resolveErr      error
	verifyErr       error
	resolveCalls    int
	verifyCalls     int
	escalateCalls   int
	escalationID    uuid.UUID
	escalationType  string
	escalationCause error
}

func (b *conflictExecutorTestBackend) LoadConflictExecutionContext(_ context.Context, id uuid.UUID) (ConflictExecutionContext, error) {
	b.input.ChangeSetID = id
	return b.input, nil
}

func (b *conflictExecutorTestBackend) ResolveConflictAttempt(_ context.Context, input ConflictExecutionContext, attempt int) (ConflictResolutionProposal, error) {
	b.resolveCalls++
	if input.AgentRole != ConflictSpecialistRole {
		return ConflictResolutionProposal{}, errors.New("specialist role was not selected")
	}
	if attempt != b.resolveCalls {
		return ConflictResolutionProposal{}, errors.New("attempt number was not monotonic")
	}
	if b.resolveErr != nil {
		return ConflictResolutionProposal{}, b.resolveErr
	}
	return b.proposal, nil
}

func (b *conflictExecutorTestBackend) VerifyConflictResolution(_ context.Context, input ConflictExecutionContext, proposal ConflictResolutionProposal) error {
	b.verifyCalls++
	if input.SourceAcceptanceCriteria == "" || input.ReviewAcceptanceCriteria == "" || !strings.Contains(input.ConflictDiff, "<<<<<<<") {
		return errors.New("specialist context omitted original acceptance evidence")
	}
	if proposal.ResolutionSHA != b.proposal.ResolutionSHA {
		return errors.New("verification received an unexpected commit")
	}
	return b.verifyErr
}

func (b *conflictExecutorTestBackend) EscalateConflict(_ context.Context, _ uuid.UUID, category string, cause error) (uuid.UUID, error) {
	b.escalateCalls++
	b.escalationType = category
	b.escalationCause = cause
	return b.escalationID, nil
}

func TestConflictResolver_AutoResolvesSimpleConflict(t *testing.T) {
	input, resolutionSHA := newConflictExecutorTestContext(t)
	backend := &conflictExecutorTestBackend{
		input: input,
		proposal: ConflictResolutionProposal{
			Resolved: true, ResolutionSHA: resolutionSHA,
			Explanation: "preserved both task requirements in the merge result",
		},
	}
	result, err := NewConflictExecutor(backend).ResolveConflict(context.Background(), input.ChangeSetID)
	if err != nil {
		t.Fatalf("ResolveConflict() error = %v", err)
	}
	if !result.Resolved || result.ResolutionSHA != resolutionSHA || result.EscalationID != nil {
		t.Fatalf("ResolveConflict() = %+v, want verified resolution %s", result, resolutionSHA)
	}
	if backend.resolveCalls != 1 || backend.verifyCalls != 1 || backend.escalateCalls != 0 {
		t.Fatalf("backend calls = resolve %d, verify %d, escalate %d; want 1, 1, 0", backend.resolveCalls, backend.verifyCalls, backend.escalateCalls)
	}
}

func TestConflictBranchRefUsesCompareAndSwap(t *testing.T) {
	input, resolutionSHA := newConflictExecutorTestContext(t)
	if err := advanceConflictBranchRef(context.Background(), input.RepoPath, "task/source", input.SourceSHA, resolutionSHA); err != nil {
		t.Fatalf("advanceConflictBranchRef() error = %v", err)
	}
	if got := conflictTestGit(t, input.RepoPath, "rev-parse", "refs/heads/task/source"); got != resolutionSHA {
		t.Fatalf("branch SHA = %s, want %s", got, resolutionSHA)
	}
	if err := advanceConflictBranchRef(context.Background(), input.RepoPath, "task/source", input.SourceSHA, input.SourceSHA); err == nil {
		t.Fatal("advanceConflictBranchRef() accepted a stale expected SHA")
	}
	if got := conflictTestGit(t, input.RepoPath, "rev-parse", "refs/heads/task/source"); got != resolutionSHA {
		t.Fatalf("stale compare-and-swap moved branch to %s, want %s", got, resolutionSHA)
	}
}

func TestConflictResolver_EscalatesComplexSemanticConflict(t *testing.T) {
	t.Run("semantic ambiguity", func(t *testing.T) {
		input, _ := newConflictExecutorTestContext(t)
		backend := &conflictExecutorTestBackend{
			input:        input,
			proposal:     ConflictResolutionProposal{Explanation: "the criteria require incompatible behavior"},
			escalationID: uuid.New(),
		}
		result, err := NewConflictExecutor(backend).ResolveConflict(context.Background(), input.ChangeSetID)
		if err != nil {
			t.Fatalf("ResolveConflict() error = %v", err)
		}
		if result.Resolved || result.EscalationID == nil || *result.EscalationID != backend.escalationID {
			t.Fatalf("ResolveConflict() = %+v, want visible escalation %s", result, backend.escalationID)
		}
		if backend.resolveCalls != projectorchestration.MaxConflictResolutionAttempts || backend.verifyCalls != 0 || backend.escalateCalls != 1 {
			t.Fatalf("backend calls = resolve %d, verify %d, escalate %d", backend.resolveCalls, backend.verifyCalls, backend.escalateCalls)
		}
		if backend.escalationType != MergeConflictEscalationCategory {
			t.Fatalf("escalation category = %q, want %q", backend.escalationType, MergeConflictEscalationCategory)
		}
	})

	t.Run("failed deterministic checks", func(t *testing.T) {
		input, resolutionSHA := newConflictExecutorTestContext(t)
		backend := &conflictExecutorTestBackend{
			input: input,
			proposal: ConflictResolutionProposal{
				Resolved: true, ResolutionSHA: resolutionSHA, Explanation: "proposed semantic fix",
			},
			verifyErr:    errors.New("unit tests failed for the proposed commit"),
			escalationID: uuid.New(),
		}
		result, err := NewConflictExecutor(backend).ResolveConflict(context.Background(), input.ChangeSetID)
		if err != nil {
			t.Fatalf("ResolveConflict() error = %v", err)
		}
		if result.Resolved || result.EscalationID == nil {
			t.Fatalf("ResolveConflict() = %+v, want escalation after failing checks", result)
		}
		if backend.resolveCalls != projectorchestration.MaxConflictResolutionAttempts || backend.verifyCalls != projectorchestration.MaxConflictResolutionAttempts || backend.escalateCalls != 1 {
			t.Fatalf("backend calls = resolve %d, verify %d, escalate %d", backend.resolveCalls, backend.verifyCalls, backend.escalateCalls)
		}
	})
}

func newConflictExecutorTestContext(t *testing.T) (ConflictExecutionContext, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable unavailable")
	}
	repo := t.TempDir()
	conflictTestGit(t, repo, "init", "-q", "-b", "main")
	conflictTestGit(t, repo, "config", "user.name", "Conflict Executor Test")
	conflictTestGit(t, repo, "config", "user.email", "conflict-test@example.invalid")
	writeConflictTestFile(t, repo, "shared.txt", "base\n")
	conflictTestGit(t, repo, "add", "shared.txt")
	conflictTestGit(t, repo, "commit", "-m", "base")

	conflictTestGit(t, repo, "switch", "-c", "task/source")
	writeConflictTestFile(t, repo, "shared.txt", "source value\n")
	conflictTestGit(t, repo, "commit", "-am", "source change")
	sourceSHA := conflictTestGit(t, repo, "rev-parse", "HEAD")
	conflictTestGit(t, repo, "switch", "main")
	writeConflictTestFile(t, repo, "shared.txt", "integration value\n")
	conflictTestGit(t, repo, "commit", "-am", "integration change")
	integrationSHA := conflictTestGit(t, repo, "rev-parse", "HEAD")
	changeSetID := uuid.New()
	worktreeParent, err := os.MkdirTemp(os.TempDir(), "multica-merge-"+changeSetID.String()+"-")
	if err != nil {
		t.Fatalf("create conflict worktree directory: %v", err)
	}
	worktree := filepath.Join(worktreeParent, "tree")
	sourceWorktree := filepath.Join(worktreeParent, "source")
	t.Cleanup(func() {
		if err := projectorchestration.CleanupConflictResolutionWorktree(context.Background(), changeSetID, repo, worktreeParent, worktree); err != nil {
			t.Errorf("clean up conflict worktree: %v", err)
			_ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", worktree).Run()
			_ = os.RemoveAll(worktreeParent)
		}
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "--force", sourceWorktree).Run()
	})
	conflictTestGit(t, repo, "worktree", "add", "--detach", sourceWorktree, sourceSHA)
	conflictTestGit(t, repo, "worktree", "add", "--detach", worktree, integrationSHA)
	if err := exec.Command("git", "-C", worktree, "merge", "--no-commit", "--no-ff", sourceSHA).Run(); err == nil {
		t.Fatal("test setup did not create a merge conflict")
	}
	writeConflictTestFile(t, worktree, "shared.txt", "<<<<<<< integration\nintegration value\n=======\nsource value\n>>>>>>> source\n")
	conflictTestGit(t, worktree, "add", "shared.txt")
	conflictTestGit(t, worktree, "commit", "-m", "resolve conflict")
	resolutionSHA := conflictTestGit(t, worktree, "rev-parse", "HEAD")

	input := ConflictExecutionContext{
		ChangeSetID: changeSetID, WorkspaceID: uuid.New(), ProjectID: uuid.New(), PlanID: uuid.New(),
		NodeID: uuid.New(), IssueID: uuid.New(), SourceTaskID: uuid.New(), ReviewTaskID: uuid.New(),
		RepoPath: repo, WorktreePath: worktree, SourceWorktreePath: sourceWorktree,
		BaseSHA: integrationSHA, SourceSHA: sourceSHA,
		ConflictFiles:            []string{"shared.txt"},
		ConflictDiff:             "<<<<<<< integration\nintegration value\n=======\nsource value\n>>>>>>> source\n",
		SourceTaskSpecification:  "Preserve the source task's existing API behavior.",
		SourceAcceptanceCriteria: "The source change remains available.",
		ReviewTaskSpecification:  "Check the source task's requested behavior.",
		ReviewAcceptanceCriteria: "The review findings are resolved.",
	}
	if err := validateConflictExecutionContext(context.Background(), input, changeSetID); err != nil {
		t.Fatalf("test conflict context is invalid: %v (worktree parent=%q temp=%q change set=%s)", err, worktreeParent, os.TempDir(), changeSetID)
	}
	return input, resolutionSHA
}

func conflictTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeConflictTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
