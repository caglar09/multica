package workflowruntime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

const (
	ConflictSpecialistRole          = "git_conflict_specialist"
	MergeConflictEscalationCategory = "merge_conflict"
	maxConflictDiffContextBytes     = 64 * 1024
	maxConflictFiles                = 256
)

var conflictCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ConflictExecutionContext is the bounded specialist context. A backend must
// load both task specifications and criteria from the recorded change set and
// include the conflict diff with its markers. Repository writes stay in the
// validated, change-set-scoped temporary worktree.
type ConflictExecutionContext struct {
	ChangeSetID  uuid.UUID
	WorkspaceID  uuid.UUID
	ProjectID    uuid.UUID
	PlanID       uuid.UUID
	NodeID       uuid.UUID
	IssueID      uuid.UUID
	SourceTaskID uuid.UUID
	ReviewTaskID uuid.UUID

	RepoPath           string
	WorktreePath       string
	SourceWorktreePath string
	BaseSHA            string
	SourceSHA          string
	ConflictFiles      []string
	ConflictDiff       string

	SourceTaskSpecification  string
	SourceAcceptanceCriteria string
	ReviewTaskSpecification  string
	ReviewAcceptanceCriteria string
	AgentRole                string
}

// ConflictResolutionProposal is the commit proposed by one specialist attempt.
// The executor validates its ancestry and deterministic checks; Runtime still
// needs fresh review and unit-test evidence before it can requeue the commit.
type ConflictResolutionProposal struct {
	Resolved      bool
	ResolutionSHA string
	Explanation   string
}

// ConflictExecutionBackend binds the autonomous specialist to repository and
// workflow services. VerifyConflictResolution must run deterministic checks on
// the exact proposed SHA and must not enqueue or advance an integration ref.
// The Runtime caller obtains fresh review and unit-test evidence after the queue
// lock is released, then uses the existing enqueue contract. Escalate must create
// a visible escalation in the supplied category.
type ConflictExecutionBackend interface {
	LoadConflictExecutionContext(ctx context.Context, changeSetID uuid.UUID) (ConflictExecutionContext, error)
	ResolveConflictAttempt(ctx context.Context, input ConflictExecutionContext, attempt int) (ConflictResolutionProposal, error)
	VerifyConflictResolution(ctx context.Context, input ConflictExecutionContext, proposal ConflictResolutionProposal) error
	EscalateConflict(ctx context.Context, changeSetID uuid.UUID, category string, cause error) (uuid.UUID, error)
}

// ConflictExecutor applies the attempt limit and deterministic verification
// before returning a proposal to the Runtime caller.
type ConflictExecutor struct {
	backend ConflictExecutionBackend
}

func NewConflictExecutor(backend ConflictExecutionBackend) *ConflictExecutor {
	return &ConflictExecutor{backend: backend}
}

func (e *ConflictExecutor) ResolveConflict(ctx context.Context, changeSetID uuid.UUID) (projectorchestration.ConflictResolutionResult, error) {
	if e == nil || e.backend == nil {
		return projectorchestration.ConflictResolutionResult{}, errors.New("conflict execution backend is not configured")
	}
	if changeSetID == uuid.Nil {
		return projectorchestration.ConflictResolutionResult{}, errors.New("change set id is required")
	}
	input, err := e.backend.LoadConflictExecutionContext(ctx, changeSetID)
	if err != nil {
		if ctx.Err() != nil {
			return projectorchestration.ConflictResolutionResult{}, ctx.Err()
		}
		return e.escalate(ctx, changeSetID, fmt.Errorf("load conflict execution context: %w", err))
	}
	if err := validateConflictExecutionContext(ctx, input, changeSetID); err != nil {
		return e.escalate(ctx, changeSetID, err)
	}
	input.AgentRole = ConflictSpecialistRole

	failures := make([]string, 0, projectorchestration.MaxConflictResolutionAttempts)
	for attempt := 1; attempt <= projectorchestration.MaxConflictResolutionAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return projectorchestration.ConflictResolutionResult{}, err
		}
		proposal, attemptErr := e.backend.ResolveConflictAttempt(ctx, input, attempt)
		if attemptErr != nil {
			failures = append(failures, fmt.Sprintf("attempt %d: %v", attempt, attemptErr))
			continue
		}
		if !proposal.Resolved {
			reason := strings.TrimSpace(proposal.Explanation)
			if reason == "" {
				reason = "specialist could not resolve the conflict"
			}
			failures = append(failures, fmt.Sprintf("attempt %d: %s", attempt, reason))
			continue
		}
		candidate := projectorchestration.ConflictResolutionResult{
			Resolved: true, ResolutionSHA: proposal.ResolutionSHA, Explanation: proposal.Explanation,
		}
		if err := projectorchestration.ValidateConflictResolutionResult(candidate); err != nil {
			failures = append(failures, fmt.Sprintf("attempt %d: %v", attempt, err))
			continue
		}
		if err := projectorchestration.ValidateConflictResolutionCommit(ctx, input.RepoPath, input.BaseSHA, input.SourceSHA, proposal.ResolutionSHA); err != nil {
			failures = append(failures, fmt.Sprintf("attempt %d: %v", attempt, err))
			continue
		}
		if err := e.backend.VerifyConflictResolution(ctx, input, proposal); err != nil {
			failures = append(failures, fmt.Sprintf("attempt %d verification: %v", attempt, err))
			continue
		}
		return candidate, nil
	}

	cause := errors.New(strings.Join(failures, "; "))
	return e.escalate(ctx, changeSetID, cause)
}

func (e *ConflictExecutor) escalate(
	ctx context.Context,
	changeSetID uuid.UUID,
	cause error,
) (projectorchestration.ConflictResolutionResult, error) {
	escalationID, err := e.backend.EscalateConflict(ctx, changeSetID, MergeConflictEscalationCategory, cause)
	if err != nil {
		return projectorchestration.ConflictResolutionResult{}, fmt.Errorf("open merge conflict escalation: %w", err)
	}
	if escalationID == uuid.Nil {
		return projectorchestration.ConflictResolutionResult{}, errors.New("merge conflict escalation backend returned an empty id")
	}
	return projectorchestration.ConflictResolutionResult{
		Explanation:  cause.Error(),
		EscalationID: &escalationID,
	}, nil
}

func validateConflictExecutionContext(ctx context.Context, input ConflictExecutionContext, expectedChangeSetID uuid.UUID) error {
	if input.ChangeSetID != expectedChangeSetID {
		return errors.New("conflict context change set does not match the requested change set")
	}
	for _, entry := range []struct {
		name string
		id   uuid.UUID
	}{
		{name: "workspace", id: input.WorkspaceID},
		{name: "project", id: input.ProjectID},
		{name: "plan", id: input.PlanID},
		{name: "node", id: input.NodeID},
		{name: "issue", id: input.IssueID},
		{name: "source task", id: input.SourceTaskID},
		{name: "review task", id: input.ReviewTaskID},
	} {
		if entry.id == uuid.Nil {
			return fmt.Errorf("conflict context %s id is required", entry.name)
		}
	}
	if strings.TrimSpace(input.RepoPath) == "" || strings.TrimSpace(input.WorktreePath) == "" || strings.TrimSpace(input.SourceWorktreePath) == "" {
		return errors.New("conflict context repository and isolated worktree paths are required")
	}
	if err := projectorchestration.ValidateConflictExecutionWorktree(ctx, expectedChangeSetID, input.RepoPath, input.WorktreePath); err != nil {
		return fmt.Errorf("validate conflict execution worktree: %w", err)
	}
	if err := validateConflictSourceWorktree(ctx, input.RepoPath, input.SourceWorktreePath); err != nil {
		return fmt.Errorf("validate conflict source worktree: %w", err)
	}
	if len(input.ConflictFiles) == 0 || len(input.ConflictFiles) > maxConflictFiles {
		return errors.New("conflict context must include between one and 256 conflicted files")
	}
	if len(input.ConflictDiff) > maxConflictDiffContextBytes {
		return errors.New("conflict context diff exceeds the safe context limit")
	}
	for _, entry := range []struct{ name, value string }{
		{name: "source task specification", value: input.SourceTaskSpecification},
		{name: "source acceptance criteria", value: input.SourceAcceptanceCriteria},
		{name: "review task specification", value: input.ReviewTaskSpecification},
		{name: "review acceptance criteria", value: input.ReviewAcceptanceCriteria},
	} {
		if strings.TrimSpace(entry.value) == "" {
			return fmt.Errorf("conflict context is missing %s", entry.name)
		}
	}
	if !conflictCommitSHA.MatchString(input.BaseSHA) || !conflictCommitSHA.MatchString(input.SourceSHA) {
		return errors.New("conflict context base and source SHAs must be valid Git commit IDs")
	}
	for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
		if !strings.Contains(input.ConflictDiff, marker) {
			return fmt.Errorf("conflict diff is missing marker %q", marker)
		}
	}
	return nil
}
