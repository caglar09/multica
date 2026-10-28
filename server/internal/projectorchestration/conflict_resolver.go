package projectorchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaxConflictResolutionAttempts = 2

// ConflictResolver proposes and deterministically verifies a conflict fix for
// a change set. A resolved result is only a proposal for the post-merge worker:
// fresh review and unit-test evidence must still be accepted by the ordinary
// enqueue contract before the change set can re-enter the merge queue. It must
// never enqueue while ProcessNext holds the project merge lock or advance an
// integration ref.
type ConflictResolver interface {
	ResolveConflict(ctx context.Context, changeSetID uuid.UUID) (ConflictResolutionResult, error)
}

// ConflictResolutionResult describes a deterministic, ancestry-checked
// conflict-fix proposal. A resolved result does not imply requeue or integration;
// fresh review and unit-test proof are required before requeue, and MergeQueue
// remains the only path that promotes an integration ref.
type ConflictResolutionResult struct {
	Resolved      bool       `json:"resolved"`
	ResolutionSHA string     `json:"resolution_sha,omitempty"`
	Explanation   string     `json:"explanation,omitempty"`
	EscalationID  *uuid.UUID `json:"escalation_id,omitempty"`
}

// ValidateConflictResolutionResult rejects incomplete or malformed agent
// output before it can be recorded as a verified resolution.
func ValidateConflictResolutionResult(result ConflictResolutionResult) error {
	if !result.Resolved {
		if strings.TrimSpace(result.ResolutionSHA) != "" {
			return errors.New("unresolved conflict result must not include a resolution SHA")
		}
		if result.EscalationID == nil || *result.EscalationID == uuid.Nil {
			return errors.New("unresolved conflict result must include a visible escalation id")
		}
		if strings.TrimSpace(result.Explanation) == "" {
			return errors.New("unresolved conflict result must include an explanation")
		}
		return nil
	}
	if result.EscalationID != nil {
		return errors.New("resolved conflict result must not include an escalation id")
	}
	if !gitCommitSHA.MatchString(result.ResolutionSHA) {
		return fmt.Errorf("conflict resolution SHA %q is invalid", result.ResolutionSHA)
	}
	if strings.TrimSpace(result.Explanation) == "" {
		return errors.New("resolved conflict result must include an explanation")
	}
	return nil
}

// ValidateConflictResolutionCommit proves the proposed commit is reachable
// from both exact sides of the conflict. It does not run quality gates or move
// refs; callers must complete fresh review and unit-test evidence checks before
// requeueing it.
func ValidateConflictResolutionCommit(
	ctx context.Context,
	repoPath, baseSHA, sourceSHA, resolutionSHA string,
) error {
	if !gitCommitSHA.MatchString(baseSHA) || !gitCommitSHA.MatchString(sourceSHA) {
		return errors.New("conflict base and source must be valid commit SHAs")
	}
	if !gitCommitSHA.MatchString(resolutionSHA) || resolutionSHA == baseSHA || resolutionSHA == sourceSHA {
		return errors.New("conflict resolution must be a new valid commit SHA")
	}
	repoRoot, err := validateRecordedRepoPath(ctx, repoPath)
	if err != nil {
		return err
	}
	baseIncluded, err := isAncestor(ctx, repoRoot, baseSHA, resolutionSHA)
	if err != nil {
		return fmt.Errorf("verify resolution commit includes conflict base: %w", err)
	}
	if !baseIncluded {
		return errors.New("conflict resolution commit does not include the recorded integration base")
	}
	sourceIncluded, err := isAncestor(ctx, repoRoot, sourceSHA, resolutionSHA)
	if err != nil {
		return fmt.Errorf("verify resolution commit includes source commit: %w", err)
	}
	if !sourceIncluded {
		return errors.New("conflict resolution commit does not include the recorded source commit")
	}
	return nil
}

// ValidateConflictExecutionWorktree ensures the specialist receives only the
// queue's isolated, change-set-scoped Git worktree from the recorded repository.
func ValidateConflictExecutionWorktree(
	ctx context.Context,
	changeSetID uuid.UUID,
	repoPath, worktreePath string,
) error {
	if changeSetID == uuid.Nil {
		return errors.New("conflict worktree change set id is required")
	}
	repoRoot, err := validateRecordedRepoPath(ctx, repoPath)
	if err != nil {
		return err
	}
	worktree, err := filepath.Abs(strings.TrimSpace(worktreePath))
	if err != nil || strings.TrimSpace(worktreePath) == "" {
		return errors.New("conflict worktree path is invalid")
	}
	changeSetUUID := pgtype.UUID{Bytes: [16]byte(changeSetID), Valid: true}
	if !safeMergeWorktreePath(filepath.Dir(worktree), worktree, changeSetUUID) {
		return errors.New("conflict worktree is not scoped to the requested change set")
	}
	worktree, err = filepath.EvalSymlinks(worktree)
	if err != nil {
		return fmt.Errorf("resolve conflict worktree: %w", err)
	}
	worktreeRoot, _, err := inspectQualityGitPath(ctx, worktree)
	if err != nil {
		return fmt.Errorf("inspect conflict worktree: %w", err)
	}
	if worktreeRoot != worktree {
		return errors.New("conflict worktree path is not its Git root")
	}
	repoCommonDir := ""
	for _, entry := range []struct{ label, dir string }{
		{label: "repository", dir: repoRoot},
		{label: "worktree", dir: worktreeRoot},
	} {
		commonDir, err := runGit(ctx, entry.dir, "rev-parse", "--git-common-dir")
		if err != nil {
			return fmt.Errorf("resolve conflict %s Git directory: %w", entry.label, err)
		}
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(entry.dir, commonDir)
		}
		commonDir, err = filepath.EvalSymlinks(commonDir)
		if err != nil {
			return fmt.Errorf("resolve conflict %s Git directory: %w", entry.label, err)
		}
		if entry.label == "repository" {
			repoCommonDir = commonDir
		} else if commonDir != repoCommonDir {
			return errors.New("conflict worktree does not belong to the recorded repository")
		}
	}
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		return errors.New("conflict worktree is not an available directory")
	}
	return nil
}

// CleanupConflictResolutionWorktree removes only a temp worktree that was
// created for this exact change set under the recorded Git repository.
func CleanupConflictResolutionWorktree(
	ctx context.Context,
	changeSetID uuid.UUID,
	repoPath, worktreeParent, worktreePath string,
) error {
	if changeSetID == uuid.Nil {
		return errors.New("conflict worktree change set id is required")
	}
	repoRoot, err := validateRecordedRepoPath(ctx, repoPath)
	if err != nil {
		return err
	}
	changeSetUUID := pgtype.UUID{Bytes: [16]byte(changeSetID), Valid: true}
	if !safeMergeWorktreePath(worktreeParent, worktreePath, changeSetUUID) {
		return errors.New("refuse to clean an unrecognized conflict worktree path")
	}
	claim := MergeQueueClaim{MergeReviewCandidate: MergeReviewCandidate{
		ChangeSetID: changeSetUUID,
		RepoPath:    repoRoot,
	}}
	return cleanupMergeWorktree(ctx, claim, map[string]any{
		"worktree_parent":      worktreeParent,
		"integration_worktree": worktreePath,
	})
}
