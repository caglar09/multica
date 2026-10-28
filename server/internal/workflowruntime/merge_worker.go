package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
)

// ProcessProjectMergeQueue is the runtime integration seam. It first verifies
// review contracts and atomically queues only candidates whose Q1 evidence is
// still current, then processes at most one change set for the project.
func (r *Runtime) ProcessProjectMergeQueue(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) (projectorchestration.MergeResult, error) {
	if r == nil || r.projectStore == nil {
		return projectorchestration.MergeResult{}, errors.New("project merge queue is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid {
		return projectorchestration.MergeResult{}, errors.New("workspace_id and project_id are required")
	}
	candidates, err := r.projectStore.ListMergeReviewCandidates(ctx, workspaceID, projectID)
	if err != nil {
		return projectorchestration.MergeResult{}, err
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.ReviewTaskID) == "" || len(candidate.ReviewResult) == 0 {
			continue
		}
		reviewTaskID, err := util.ParseUUID(candidate.ReviewTaskID)
		if err != nil || reviewTaskID == candidate.SourceTaskID {
			continue
		}
		verdict, err := parseReviewVerdict(candidate.ReviewResult)
		if err != nil || verdict.Verdict != "approved" {
			continue
		}
		if _, err := r.projectStore.EnqueueReviewedChangeSet(ctx, candidate, reviewTaskID); err != nil {
			return projectorchestration.MergeResult{}, err
		}
	}
	queue := projectorchestration.NewMergeQueue(r.projectStore, r.qualityGateRunner).
		WithConflictResolver(NewConflictExecutor(r))
	result, outcome, err := queue.ProcessNextWithConflictResolution(ctx, workspaceID, projectID)
	if err != nil || outcome == nil || outcome.Status != "resolution_pending_review" {
		return result, err
	}
	if err := r.requeueConflictResolutionForReview(ctx, *outcome); err != nil {
		if _, escalateErr := r.EscalateConflict(ctx, outcome.ChangeSetID, MergeConflictEscalationCategory, err); escalateErr != nil {
			return result, errors.Join(err, fmt.Errorf("create visible conflict escalation: %w", escalateErr))
		}
		return result, fmt.Errorf("queue fresh conflict resolution review: %w", err)
	}
	return result, nil
}

func (r *Runtime) requeueConflictResolutionForReview(ctx context.Context, outcome projectorchestration.MergeQueueConflictOutcome) error {
	if r == nil || r.pool == nil || r.taskSvc == nil || !outcome.WorkspaceID.Valid || !outcome.ProjectID.Valid ||
		outcome.ChangeSetID == uuid.Nil || !conflictCommitSHA.MatchString(outcome.OriginalCommitSHA) ||
		!conflictCommitSHA.MatchString(outcome.ResolutionSHA) || strings.TrimSpace(outcome.BranchName) == "" {
		return errors.New("conflict resolution review context is incomplete")
	}
	var reviewIssueID, ownerUserID, oldReviewArtifactID pgtype.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT review_node.materialized_issue_id, team.owner_user_id,
		       COALESCE((
		           SELECT artifact.id
		           FROM autonomous_project_artifact artifact
		           WHERE artifact.plan_id=c.plan_id AND artifact.node_id=review_node.id
		             AND artifact.artifact_type='review' AND artifact.status='active'
		           ORDER BY artifact.created_at DESC, artifact.id DESC
	           LIMIT 1
	       ),'00000000-0000-0000-0000-000000000000'::uuid)
		FROM autonomous_project_change_set c
		JOIN autonomous_project_plan_node source_node
		  ON source_node.id=c.node_id AND source_node.plan_id=c.plan_id
		 AND source_node.workspace_id=c.workspace_id AND source_node.project_id=c.project_id
		JOIN autonomous_project_plan_edge edge
		  ON edge.plan_id=c.plan_id AND edge.workspace_id=c.workspace_id
		 AND edge.project_id=c.project_id AND edge.from_node_key=source_node.node_key
		JOIN autonomous_project_plan_node review_node
		  ON review_node.plan_id=edge.plan_id AND review_node.workspace_id=edge.workspace_id
		 AND review_node.project_id=edge.project_id AND review_node.node_key=edge.to_node_key
		 AND review_node.kind='review' AND review_node.status='completed'
		 AND review_node.materialized_issue_id IS NOT NULL
		JOIN autonomous_project_team team
		  ON team.workspace_id=c.workspace_id AND team.project_id=c.project_id AND team.status='active'
		WHERE c.id=$1 AND c.workspace_id=$2 AND c.project_id=$3 AND c.merge_status='conflict'
		ORDER BY review_node.updated_at DESC, review_node.id
		LIMIT 1
	`, pgtype.UUID{Bytes: [16]byte(outcome.ChangeSetID), Valid: true}, outcome.WorkspaceID, outcome.ProjectID).
		Scan(&reviewIssueID, &ownerUserID, &oldReviewArtifactID)
	if err != nil {
		return fmt.Errorf("load conflict review issue: %w", err)
	}
	if !reviewIssueID.Valid || !ownerUserID.Valid {
		return errors.New("conflict review issue or accountable user is missing")
	}
	issue, err := r.taskSvc.Queries.GetIssue(ctx, reviewIssueID)
	if err != nil {
		return fmt.Errorf("load conflict review issue for dispatch: %w", err)
	}
	if issue.WorkspaceID != outcome.WorkspaceID || issue.ProjectID != outcome.ProjectID {
		return errors.New("conflict review issue escaped its workspace or project")
	}

	if err := advanceConflictBranchRef(ctx, outcome.RepoPath, outcome.BranchName, outcome.OriginalCommitSHA, outcome.ResolutionSHA); err != nil {
		return err
	}
	resolutionContext, _ := json.Marshal(map[string]any{
		"conflict_resolution_sha": outcome.ResolutionSHA,
		"original_commit_sha":     outcome.OriginalCommitSHA,
		"integration_base_sha":    outcome.IntegrationBaseSHA,
		"source_task_id":          util.UUIDToString(outcome.SourceTaskID),
		"previous_review_task_id": util.UUIDToString(outcome.PreviousReviewTaskID),
	})
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		_ = advanceConflictBranchRef(context.Background(), outcome.RepoPath, outcome.BranchName, outcome.ResolutionSHA, outcome.OriginalCommitSHA)
		return fmt.Errorf("begin conflict review handoff: %w", err)
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET commit_sha=$4, merge_status='ready', ready_at=now(),
		    merge_order=NULL, approved_review_task_id=NULL, approved_review_artifact_id=NULL,
		    merge_evidence=COALESCE(merge_evidence,'{}'::jsonb) || $5::jsonb,
		    updated_at=now()
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
		  AND merge_status='conflict' AND commit_sha=$6 AND branch_name=$7
	`, pgtype.UUID{Bytes: [16]byte(outcome.ChangeSetID), Valid: true}, outcome.WorkspaceID,
		outcome.ProjectID, outcome.ResolutionSHA, resolutionContext, outcome.OriginalCommitSHA, outcome.BranchName)
	if err != nil || tag.RowsAffected() != 1 {
		_ = advanceConflictBranchRef(context.Background(), outcome.RepoPath, outcome.BranchName, outcome.ResolutionSHA, outcome.OriginalCommitSHA)
		if err != nil {
			return fmt.Errorf("record verified conflict resolution: %w", err)
		}
		return errors.New("change set moved before conflict resolution could be requeued")
	}
	if oldReviewArtifactID.Valid && oldReviewArtifactID.Bytes != [16]byte{} {
		if _, err := tx.Exec(ctx, `
			UPDATE autonomous_project_artifact
			SET status='superseded'
			WHERE id=$1 AND status='active'
		`, oldReviewArtifactID); err != nil {
			_ = advanceConflictBranchRef(context.Background(), outcome.RepoPath, outcome.BranchName, outcome.ResolutionSHA, outcome.OriginalCommitSHA)
			return fmt.Errorf("invalidate review for original conflict commit: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		_ = advanceConflictBranchRef(context.Background(), outcome.RepoPath, outcome.BranchName, outcome.ResolutionSHA, outcome.OriginalCommitSHA)
		return fmt.Errorf("commit conflict resolution review handoff: %w", err)
	}

	handoff := fmt.Sprintf(
		"Review the conflict resolution at exact commit %s. Compare it with original source commit %s and integration base %s. Inspect the combined source task and review acceptance criteria, and return the structured autonomous review verdict with goal_alignment_score and exact requirement_attestations. Do not approve based on the prior review; this commit requires a fresh review.",
		outcome.ResolutionSHA, outcome.OriginalCommitSHA, outcome.IntegrationBaseSHA,
	)
	if _, err := r.taskSvc.EnqueueTaskForIssueWithHandoff(ctx, issue, handoff, ownerUserID); err != nil {
		_, _ = r.pool.Exec(context.Background(), `
			UPDATE autonomous_project_change_set
			SET merge_status='conflict', merge_evidence=COALESCE(merge_evidence,'{}'::jsonb) ||
			    jsonb_build_object('review_enqueue_error',$2), updated_at=now()
			WHERE id=$1 AND commit_sha=$3 AND merge_status='ready'
		`, pgtype.UUID{Bytes: [16]byte(outcome.ChangeSetID), Valid: true}, err.Error(), outcome.ResolutionSHA)
		return fmt.Errorf("enqueue fresh conflict resolution review task: %w", err)
	}
	return nil
}

func advanceConflictBranchRef(ctx context.Context, repoPath, branchName, expectedSHA, nextSHA string) error {
	if strings.TrimSpace(repoPath) == "" || strings.TrimSpace(branchName) == "" ||
		!conflictCommitSHA.MatchString(expectedSHA) || !conflictCommitSHA.MatchString(nextSHA) {
		return errors.New("conflict branch update context is incomplete")
	}
	ref := "refs/heads/" + branchName
	if _, err := gitCommand(ctx, repoPath, "check-ref-format", ref); err != nil {
		return fmt.Errorf("validate conflict source branch: %w", err)
	}
	if _, err := gitCommand(ctx, repoPath, "update-ref", "--no-deref", ref, nextSHA, expectedSHA); err != nil {
		return fmt.Errorf("advance conflict source branch with compare-and-swap: %w", err)
	}
	return nil
}

func (r *Runtime) runMergeQueueWorker(ctx context.Context) {
	interval := r.config.PollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.processMergeQueueProjects(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("autonomous project merge queue pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) processMergeQueueProjects(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT workspace_id, project_id
		FROM autonomous_project_change_set
		WHERE merge_status IN ('ready','queued','merging')
		ORDER BY workspace_id, project_id
	`)
	if err != nil {
		return fmt.Errorf("list projects with merge queue work: %w", err)
	}
	type projectRef struct {
		workspaceID pgtype.UUID
		projectID   pgtype.UUID
	}
	projects := make([]projectRef, 0)
	for rows.Next() {
		var project projectRef
		if err := rows.Scan(&project.workspaceID, &project.projectID); err != nil {
			rows.Close()
			return fmt.Errorf("scan project merge queue work: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate project merge queue work: %w", err)
	}
	rows.Close()

	for _, project := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := r.ProcessProjectMergeQueue(ctx, project.workspaceID, project.projectID)
		if err == nil || errors.Is(err, projectorchestration.ErrMergeQueueEmpty) || errors.Is(err, projectorchestration.ErrMergeQueueBusy) {
			continue
		}
		slog.Warn("process autonomous project merge queue item failed",
			"workspace_id", util.UUIDToString(project.workspaceID),
			"project_id", util.UUIDToString(project.projectID),
			"error", err)
	}
	return nil
}
