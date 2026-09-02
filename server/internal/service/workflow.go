package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SetIssueStatusForWorkflow is the autonomous-orchestration write path for an
// issue status. It deliberately goes through the same status catalog validation
// and realtime broadcast used by other server-owned status transitions.
func (s *TaskService) SetIssueStatusForWorkflow(ctx context.Context, issueID pgtype.UUID, status string) (db.Issue, error) {
	if s == nil || s.Queries == nil {
		return db.Issue{}, fmt.Errorf("task service is not configured")
	}
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return db.Issue{}, fmt.Errorf("load workflow issue: %w", err)
	}
	if issue.Status == status {
		return issue, nil
	}
	if _, err := issuestatus.Resolve(ctx, s.Queries, issue.WorkspaceID, status); err != nil {
		return db.Issue{}, fmt.Errorf("resolve workflow issue status %q: %w", status, err)
	}

	updated, err := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID:          issue.ID,
		Status:      status,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("update workflow issue status: %w", err)
	}
	s.broadcastIssueUpdated(ctx, updated, issue.Status)
	return updated, nil
}

// EnqueueTaskForWorkflow hands a durable workflow action to Multica's normal
// task admission path. This is intentionally a wrapper around
// enqueueMentionTask rather than a direct agent_task_queue insert: runtime
// readiness, attribution policy, duplicate-pending protection, task broadcasts
// and daemon wakeups therefore stay identical to ordinary agent dispatch.
//
// accountableUserID is the workflow's stable top-level human. When absent the
// existing attribution fallback policy remains authoritative.
func (s *TaskService) EnqueueTaskForWorkflow(
	ctx context.Context,
	issue db.Issue,
	agentID pgtype.UUID,
	accountableUserID pgtype.UUID,
	handoffNote string,
) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTask(
		ctx,
		issue,
		agentID,
		pgtype.UUID{}, // no synthetic comment: the workflow event is the trigger
		false,
		pgtype.UUID{},
		false,
		handoffNote,
		accountableUserID,
		pgtype.UUID{},
	)
}
