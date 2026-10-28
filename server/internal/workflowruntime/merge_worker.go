package workflowruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	return projectorchestration.NewMergeQueue(r.projectStore, r.qualityGateRunner).ProcessNext(ctx, workspaceID, projectID)
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
