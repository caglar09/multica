package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
)

func (r *Runtime) LoadConflictExecutionContext(ctx context.Context, changeSetID uuid.UUID) (ConflictExecutionContext, error) {
	if r == nil || r.pool == nil || changeSetID == uuid.Nil {
		return ConflictExecutionContext{}, errors.New("conflict execution storage is unavailable")
	}
	var changeID, workspaceID, projectID, planID, nodeID, issueID, sourceTaskID, reviewTaskID pgtype.UUID
	var repoPath, sourceWorktreePath, worktreePath, baseSHA, sourceSHA string
	var conflictFilesRaw, acceptanceRaw, reviewAcceptanceRaw []byte
	var conflictDiff, sourceSpecification, reviewSpecification string
	err := r.pool.QueryRow(ctx, `
		SELECT c.id, c.workspace_id, c.project_id, c.plan_id, c.node_id, c.issue_id,
		       c.source_task_id, c.approved_review_task_id,
		       COALESCE(c.repo_path,''), COALESCE(c.worktree_path,''),
		       COALESCE(c.merge_evidence->>'integration_worktree',''),
		       COALESCE(c.merge_evidence->>'integration_base_sha',''), c.commit_sha,
		       COALESCE(c.merge_evidence->'conflicts','[]'::jsonb),
		       COALESCE(c.merge_evidence->>'conflict_diff',''),
	       source_node.description, source_node.acceptance_criteria::text,
	       review_node.description, review_node.acceptance_criteria::text
		FROM autonomous_project_change_set c
		JOIN autonomous_project_plan_node source_node
		  ON source_node.id=c.node_id AND source_node.plan_id=c.plan_id
		 AND source_node.workspace_id=c.workspace_id AND source_node.project_id=c.project_id
		JOIN autonomous_project_plan_edge e
		  ON e.plan_id=c.plan_id AND e.workspace_id=c.workspace_id
		 AND e.project_id=c.project_id AND e.from_node_key=source_node.node_key
		JOIN autonomous_project_plan_node review_node
		  ON review_node.plan_id=e.plan_id AND review_node.workspace_id=e.workspace_id
		 AND review_node.project_id=e.project_id AND review_node.node_key=e.to_node_key
		 AND review_node.kind='review' AND review_node.status='completed'
		WHERE c.id=$1 AND c.merge_status='merging'
		ORDER BY review_node.updated_at DESC, review_node.id
		LIMIT 1
	`, pgtype.UUID{Bytes: [16]byte(changeSetID), Valid: true}).Scan(
		&changeID, &workspaceID, &projectID, &planID, &nodeID, &issueID,
		&sourceTaskID, &reviewTaskID, &repoPath, &sourceWorktreePath, &worktreePath,
		&baseSHA, &sourceSHA, &conflictFilesRaw, &conflictDiff,
		&sourceSpecification, &acceptanceRaw, &reviewSpecification, &reviewAcceptanceRaw,
	)
	if err != nil {
		return ConflictExecutionContext{}, fmt.Errorf("load conflict change-set context: %w", err)
	}
	var conflictFiles []string
	if err := json.Unmarshal(conflictFilesRaw, &conflictFiles); err != nil {
		return ConflictExecutionContext{}, fmt.Errorf("decode conflict file list: %w", err)
	}
	return ConflictExecutionContext{
		ChangeSetID: uuid.UUID(changeID.Bytes), WorkspaceID: uuid.UUID(workspaceID.Bytes),
		ProjectID: uuid.UUID(projectID.Bytes), PlanID: uuid.UUID(planID.Bytes),
		NodeID: uuid.UUID(nodeID.Bytes), IssueID: uuid.UUID(issueID.Bytes),
		SourceTaskID: uuid.UUID(sourceTaskID.Bytes), ReviewTaskID: uuid.UUID(reviewTaskID.Bytes),
		RepoPath: repoPath, WorktreePath: worktreePath, SourceWorktreePath: sourceWorktreePath,
		BaseSHA: baseSHA, SourceSHA: sourceSHA, ConflictFiles: conflictFiles, ConflictDiff: conflictDiff,
		SourceTaskSpecification: sourceSpecification, SourceAcceptanceCriteria: string(acceptanceRaw),
		ReviewTaskSpecification: reviewSpecification, ReviewAcceptanceCriteria: string(reviewAcceptanceRaw),
	}, nil
}

func (r *Runtime) ResolveConflictAttempt(ctx context.Context, input ConflictExecutionContext, attempt int) (ConflictResolutionProposal, error) {
	if r == nil || r.conflictPatchResolver == nil {
		return ConflictResolutionProposal{}, projectorchestration.ErrAdapterNotConfigured
	}
	proposal, err := r.conflictPatchResolver.ResolvePatch(ctx, projectorchestration.ConflictPatchRequest{
		ChangeSetID: input.ChangeSetID.String(), WorkspaceID: input.WorkspaceID.String(),
		ProjectID: input.ProjectID.String(), PlanID: input.PlanID.String(), NodeID: input.NodeID.String(),
		IssueID: input.IssueID.String(), SourceTaskID: input.SourceTaskID.String(),
		ReviewTaskID: input.ReviewTaskID.String(), Attempt: attempt, AgentRole: ConflictSpecialistRole,
		BaseSHA: input.BaseSHA, SourceSHA: input.SourceSHA, ConflictFiles: append([]string(nil), input.ConflictFiles...),
		ConflictDiff: input.ConflictDiff, SourceTaskSpecification: input.SourceTaskSpecification,
		SourceAcceptanceCriteria: input.SourceAcceptanceCriteria,
		ReviewTaskSpecification:  input.ReviewTaskSpecification,
		ReviewAcceptanceCriteria: input.ReviewAcceptanceCriteria,
	})
	if err != nil {
		return ConflictResolutionProposal{}, err
	}
	if !proposal.Resolved {
		return ConflictResolutionProposal{Explanation: proposal.Explanation}, nil
	}
	if err := validateConflictPatchPaths(proposal.Patch, input.ConflictFiles); err != nil {
		return ConflictResolutionProposal{}, err
	}
	if attempt > 1 {
		if err := restartConflictMerge(ctx, input); err != nil {
			return ConflictResolutionProposal{}, fmt.Errorf("restart isolated conflict attempt: %w", err)
		}
	}
	if err := applyConflictPatch(ctx, input, proposal.Patch); err != nil {
		return ConflictResolutionProposal{}, err
	}
	commitSHA, err := commitConflictResolution(ctx, input)
	if err != nil {
		return ConflictResolutionProposal{}, err
	}
	return ConflictResolutionProposal{Resolved: true, ResolutionSHA: commitSHA, Explanation: proposal.Explanation}, nil
}

func (r *Runtime) VerifyConflictResolution(ctx context.Context, input ConflictExecutionContext, proposal ConflictResolutionProposal) error {
	if r == nil || r.qualityGateRunner == nil {
		return projectorchestration.ErrQualityGateUnavailable
	}
	for _, gateType := range []string{"build", "integration_test"} {
		request := conflictQualityRequest(input, input.WorktreePath, proposal.ResolutionSHA, gateType)
		if _, status, err := evaluateQualityGate(ctx, r.qualityGateRunner, request); err != nil {
			return fmt.Errorf("conflict resolution %s gate %s: %w", status, gateType, err)
		}
	}
	if err := setConflictSourceWorktreeCommit(ctx, input, proposal.ResolutionSHA); err != nil {
		return err
	}
	request := conflictQualityRequest(input, input.SourceWorktreePath, proposal.ResolutionSHA, "unit_test")
	result, status, gateErr := evaluateQualityGate(ctx, r.qualityGateRunner, request)
	qualityContext := nodeQualityContext{
		WorkspaceID: pgtype.UUID{Bytes: [16]byte(input.WorkspaceID), Valid: true},
		ProjectID:   pgtype.UUID{Bytes: [16]byte(input.ProjectID), Valid: true},
		PlanID:      pgtype.UUID{Bytes: [16]byte(input.PlanID), Valid: true},
		NodeID:      pgtype.UUID{Bytes: [16]byte(input.NodeID), Valid: true},
		IssueID:     pgtype.UUID{Bytes: [16]byte(input.IssueID), Valid: true},
		Kind:        projectorchestration.NodeImplementation,
	}
	evidence := map[string]any{
		"mode": "deterministic_runner", "runner_evidence": result.Evidence,
		"source_task_id": input.SourceTaskID.String(), "conflict_resolution_sha": proposal.ResolutionSHA,
	}
	if gateErr != nil {
		if persistErr := r.recordPolicyGate(ctx, qualityContext, "unit_test", status, gateErr.Error(), evidence); persistErr != nil {
			return errors.Join(gateErr, fmt.Errorf("persist conflict unit-test evidence: %w", persistErr))
		}
		return fmt.Errorf("conflict resolution unit_test gate %s: %w", status, gateErr)
	}
	if err := r.recordPolicyGate(ctx, qualityContext, "unit_test", "passed", "", evidence); err != nil {
		return fmt.Errorf("persist conflict unit-test evidence: %w", err)
	}
	return nil
}

func (r *Runtime) EscalateConflict(ctx context.Context, changeSetID uuid.UUID, category string, cause error) (uuid.UUID, error) {
	if r == nil || r.pool == nil {
		return uuid.Nil, errors.New("conflict escalation storage is unavailable")
	}
	if strings.TrimSpace(category) != MergeConflictEscalationCategory || cause == nil {
		return uuid.Nil, errors.New("conflict escalation category and cause are required")
	}
	var escalationID pgtype.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, plan_id, node_id, category, severity, summary, context
		)
		SELECT c.workspace_id, c.project_id, c.plan_id, c.node_id, 'merge_conflict', 'high',
		       'Autonomous merge conflict requires human resolution',
		       jsonb_build_object('change_set_id',c.id::text,'source_task_id',c.source_task_id::text,'error',$2)
		FROM autonomous_project_change_set c
		WHERE c.id=$1
		RETURNING id
	`, pgtype.UUID{Bytes: [16]byte(changeSetID), Valid: true}, cause.Error()).Scan(&escalationID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create merge conflict escalation: %w", err)
	}
	return uuid.UUID(escalationID.Bytes), nil
}

func conflictQualityRequest(input ConflictExecutionContext, worktreePath, commitSHA, gateType string) projectorchestration.QualityGateRequest {
	return projectorchestration.QualityGateRequest{
		WorkspaceID: pgtype.UUID{Bytes: [16]byte(input.WorkspaceID), Valid: true},
		ProjectID:   pgtype.UUID{Bytes: [16]byte(input.ProjectID), Valid: true},
		PlanID:      pgtype.UUID{Bytes: [16]byte(input.PlanID), Valid: true},
		NodeID:      pgtype.UUID{Bytes: [16]byte(input.NodeID), Valid: true},
		IssueID:     pgtype.UUID{Bytes: [16]byte(input.IssueID), Valid: true},
		TaskID:      pgtype.UUID{Bytes: [16]byte(input.SourceTaskID), Valid: true},
		GateType:    gateType, WorktreePath: worktreePath, RepoPath: input.RepoPath,
		CommitSHA: commitSHA, Artifact: map[string]any{"conflict_resolution": true},
	}
}

func validateConflictSourceWorktree(ctx context.Context, repoPath, worktreePath string) error {
	repoPath, err := filepath.EvalSymlinks(strings.TrimSpace(repoPath))
	if err != nil {
		return fmt.Errorf("resolve recorded repository path: %w", err)
	}
	worktreePath, err = filepath.EvalSymlinks(strings.TrimSpace(worktreePath))
	if err != nil {
		return fmt.Errorf("resolve source task worktree: %w", err)
	}
	repoRoot, err := gitOutput(ctx, repoPath, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(repoRoot) != filepath.Clean(repoPath) {
		return errors.New("recorded repository path is not a Git repository root")
	}
	worktreeRoot, err := gitOutput(ctx, worktreePath, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(worktreeRoot) != filepath.Clean(worktreePath) {
		return errors.New("recorded source path is not a Git worktree root")
	}
	repoCommon, err := gitCommonDir(ctx, repoPath)
	if err != nil {
		return err
	}
	worktreeCommon, err := gitCommonDir(ctx, worktreePath)
	if err != nil {
		return err
	}
	if filepath.Clean(repoCommon) != filepath.Clean(worktreeCommon) {
		return errors.New("source task worktree belongs to a different Git repository")
	}
	return nil
}

func gitCommonDir(ctx context.Context, dir string) (string, error) {
	common, err := gitOutput(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	resolved, err := filepath.EvalSymlinks(common)
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func setConflictSourceWorktreeCommit(ctx context.Context, input ConflictExecutionContext, commitSHA string) error {
	if err := validateConflictSourceWorktree(ctx, input.RepoPath, input.SourceWorktreePath); err != nil {
		return err
	}
	status, err := gitOutput(ctx, input.SourceWorktreePath, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect source worktree before conflict verification: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("source task worktree contains uncommitted files; refusing to overwrite it for conflict verification")
	}
	if _, err := gitCommand(ctx, input.SourceWorktreePath, "checkout", "--detach", commitSHA); err != nil {
		return fmt.Errorf("check out exact conflict resolution commit for unit tests: %w", err)
	}
	got, err := gitOutput(ctx, input.SourceWorktreePath, "rev-parse", "HEAD")
	if err != nil || got != commitSHA {
		return errors.New("source task worktree did not check out the exact conflict resolution commit")
	}
	return nil
}

func restartConflictMerge(ctx context.Context, input ConflictExecutionContext) error {
	worktreePath := input.WorktreePath
	if _, err := gitCommand(ctx, worktreePath, "merge", "--abort"); err != nil {
		// A failed first patch may have staged a resolved index; removal below
		// resets that isolated worktree without touching the source task worktree.
	}
	if _, err := gitCommand(ctx, input.RepoPath, "worktree", "remove", "--force", worktreePath); err != nil {
		return fmt.Errorf("remove prior conflict attempt worktree: %w", err)
	}
	if _, err := gitCommand(ctx, input.RepoPath, "worktree", "add", "--detach", worktreePath, input.BaseSHA); err != nil {
		return fmt.Errorf("recreate conflict attempt worktree: %w", err)
	}
	hooksPath := filepath.Join(filepath.Dir(worktreePath), "empty-hooks")
	if _, err := gitCommand(ctx, worktreePath, "-c", "core.hooksPath="+hooksPath, "merge", "--no-commit", "--no-ff", input.SourceSHA); err != nil {
		unmerged, listErr := gitCommand(ctx, worktreePath, "ls-files", "-u", "-z")
		if listErr != nil || len(unmerged) == 0 {
			return fmt.Errorf("recreate the recorded conflict: %w", err)
		}
	}
	return nil
}

func validateConflictPatchPaths(patch string, conflictFiles []string) error {
	allowed := make(map[string]struct{}, len(conflictFiles))
	for _, file := range conflictFiles {
		if !safeConflictPath(file) {
			return fmt.Errorf("unsafe conflicted file path %q", file)
		}
		allowed[file] = struct{}{}
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		parts := strings.Fields(strings.TrimPrefix(line, "diff --git "))
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "a/") || !strings.HasPrefix(parts[1], "b/") {
			return errors.New("conflict patch must use unquoted, repository-relative Git diff paths")
		}
		from, to := strings.TrimPrefix(parts[0], "a/"), strings.TrimPrefix(parts[1], "b/")
		if !safeConflictPath(from) || !safeConflictPath(to) {
			return errors.New("conflict patch contains an unsafe path")
		}
		if _, ok := allowed[from]; !ok {
			return fmt.Errorf("conflict patch changes an unapproved file %q", from)
		}
		if _, ok := allowed[to]; !ok {
			return fmt.Errorf("conflict patch changes an unapproved file %q", to)
		}
		seen[to] = struct{}{}
	}
	if len(seen) == 0 {
		return errors.New("conflict patch contains no Git file changes")
	}
	return nil
}

func safeConflictPath(value string) bool {
	if value == "" || strings.HasPrefix(value, ":") || strings.ContainsRune(value, '\x00') || filepath.IsAbs(value) {
		return false
	}
	clean := filepath.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func applyConflictPatch(ctx context.Context, input ConflictExecutionContext, patch string) error {
	if _, err := gitCommandInput(ctx, input.WorktreePath, strings.NewReader(patch), "apply", "--whitespace=error", "-"); err != nil {
		return fmt.Errorf("apply specialist conflict patch: %w", err)
	}
	args := append([]string{"--literal-pathspecs", "add", "--"}, input.ConflictFiles...)
	if _, err := gitCommand(ctx, input.WorktreePath, args...); err != nil {
		return fmt.Errorf("stage resolved conflict files: %w", err)
	}
	unmerged, err := gitCommand(ctx, input.WorktreePath, "ls-files", "-u", "-z")
	if err != nil || len(unmerged) != 0 {
		return errors.New("specialist patch left unresolved Git index entries")
	}
	if _, err := gitCommand(ctx, input.WorktreePath, "diff", "--cached", "--check"); err != nil {
		return fmt.Errorf("check resolved conflict whitespace: %w", err)
	}
	return nil
}

func commitConflictResolution(ctx context.Context, input ConflictExecutionContext) (string, error) {
	hooksPath := filepath.Join(filepath.Dir(input.WorktreePath), "empty-hooks")
	if _, err := gitCommand(ctx, input.WorktreePath,
		"-c", "core.hooksPath="+hooksPath,
		"-c", "user.name=Multica Conflict Resolver",
		"-c", "user.email=noreply@multica.ai",
		"commit", "-m", "Resolve autonomous merge conflict"); err != nil {
		return "", fmt.Errorf("commit verified conflict resolution: %w", err)
	}
	commitSHA, err := gitOutput(ctx, input.WorktreePath, "rev-parse", "HEAD")
	if err != nil || !conflictCommitSHA.MatchString(commitSHA) {
		return "", errors.New("conflict resolver produced an invalid commit SHA")
	}
	return commitSHA, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := gitCommand(ctx, dir, args...)
	return strings.TrimSpace(string(output)), err
}

func gitCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return gitCommandInput(ctx, dir, nil, args...)
}

func gitCommandInput(ctx context.Context, dir string, input interface{ Read([]byte) (int, error) }, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if input != nil {
		command.Stdin = input
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func projectConflictChangeSetID(value pgtype.UUID) string {
	return util.UUIDToString(value)
}
