package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
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

// EnqueueTaskForWorkflow hands autonomous work to Multica's normal task
// admission path. Project-bound tasks are context-compiled before admission.
// Durable workflow actions reuse their action-bound package; direct Project OS
// stages receive the same bounded policy through an action-less compilation
// receipt. Any compiler failure is fail-closed.
func (s *TaskService) EnqueueTaskForWorkflow(
	ctx context.Context,
	issue db.Issue,
	agentID pgtype.UUID,
	accountableUserID pgtype.UUID,
	handoffNote string,
) (db.AgentTaskQueue, error) {
	var contextActionID pgtype.UUID
	var contextCompilationID pgtype.UUID
	if issue.ProjectID.Valid {
		if s == nil || s.TxStarter == nil {
			return db.AgentTaskQueue{}, fmt.Errorf("project workflow context compiler requires transaction support")
		}

		trimmedHandoff := strings.TrimSpace(handoffNote)
		if strings.HasPrefix(trimmedHandoff, "[workflow-action:") {
			actionID, kind, envelope, marker, err := parseWorkflowContextEnvelope(handoffNote)
			if err != nil {
				return db.AgentTaskQueue{}, fmt.Errorf("parse workflow context envelope: %w", err)
			}
			tx, err := s.TxStarter.Begin(ctx)
			if err != nil {
				return db.AgentTaskQueue{}, fmt.Errorf("begin workflow context compilation: %w", err)
			}
			pkg, compileErr := projectorchestration.CompileWorkflowContext(ctx, tx, issue, agentID, actionID, kind)
			if compileErr != nil {
				_ = tx.Rollback(ctx)
				return db.AgentTaskQueue{}, fmt.Errorf("compile bounded project context: %w", compileErr)
			}
			envelope["context_package"] = pkg
			raw, err := json.Marshal(envelope)
			if err != nil {
				_ = tx.Rollback(ctx)
				return db.AgentTaskQueue{}, fmt.Errorf("encode bounded project context: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return db.AgentTaskQueue{}, fmt.Errorf("commit workflow context compilation: %w", err)
			}
			handoffNote = marker + "\n" + string(raw)
			contextActionID = actionID
		} else {
			tx, err := s.TxStarter.Begin(ctx)
			if err != nil {
				return db.AgentTaskQueue{}, fmt.Errorf("begin direct project context compilation: %w", err)
			}
			compiled, compileErr := projectorchestration.CompileDirectProjectContext(
				ctx, tx, issue, agentID, "project_stage_assignment",
			)
			if compileErr != nil {
				_ = tx.Rollback(ctx)
				return db.AgentTaskQueue{}, fmt.Errorf("compile bounded direct project context: %w", compileErr)
			}
			envelope := map[string]any{
				"kind":            "project_stage_assignment",
				"note":            trimmedHandoff,
				"context_package": compiled.Package,
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				_ = tx.Rollback(ctx)
				return db.AgentTaskQueue{}, fmt.Errorf("encode bounded direct project context: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return db.AgentTaskQueue{}, fmt.Errorf("commit direct project context compilation: %w", err)
			}
			handoffNote = "[project-context]\n" + string(raw)
			contextCompilationID = compiled.ID
		}
	}

	queued, err := s.enqueueMentionTask(
		ctx,
		issue,
		agentID,
		pgtype.UUID{}, // no synthetic comment: the workflow/project event is the trigger
		false,
		pgtype.UUID{},
		false,
		handoffNote,
		accountableUserID,
		pgtype.UUID{},
	)
	if err != nil {
		if contextCompilationID.Valid {
			if cleanupErr := s.discardDirectContextCompilation(ctx, contextCompilationID); cleanupErr != nil {
				slog.Warn("failed to discard unbound direct project context",
					"context_compilation_id", util.UUIDToString(contextCompilationID),
					"error", cleanupErr,
				)
			}
		}
		return queued, err
	}
	if contextActionID.Valid && queued.ID.Valid {
		if err := s.bindWorkflowContextTask(ctx, contextActionID, queued.ID); err != nil {
			// The task already contains the durable action marker + compiled package,
			// so binding failure must not enqueue a duplicate on action retry. The
			// workflow_action_id remains an authoritative audit join until repair.
			slog.Warn("workflow context task binding failed",
				"workflow_action_id", util.UUIDToString(contextActionID),
				"task_id", util.UUIDToString(queued.ID),
				"error", err,
			)
		}
	}
	if contextCompilationID.Valid && queued.ID.Valid {
		if err := s.bindDirectContextTask(ctx, contextCompilationID, queued.ID); err != nil {
			// The admitted task already embeds the package. Keep execution safe and
			// surface the audit-link repair separately instead of duplicating work.
			slog.Warn("direct project context task binding failed",
				"context_compilation_id", util.UUIDToString(contextCompilationID),
				"task_id", util.UUIDToString(queued.ID),
				"error", err,
			)
		}
	}
	return queued, nil
}

func parseWorkflowContextEnvelope(handoffNote string) (pgtype.UUID, string, map[string]any, string, error) {
	parts := strings.SplitN(strings.TrimSpace(handoffNote), "\n", 2)
	if len(parts) != 2 {
		return pgtype.UUID{}, "", nil, "", fmt.Errorf("workflow handoff is missing action marker or structured envelope")
	}
	marker := strings.TrimSpace(parts[0])
	const prefix = "[workflow-action:"
	if !strings.HasPrefix(marker, prefix) || !strings.HasSuffix(marker, "]") {
		return pgtype.UUID{}, "", nil, "", fmt.Errorf("workflow handoff marker is malformed")
	}
	actionID, err := util.ParseUUID(strings.TrimSuffix(strings.TrimPrefix(marker, prefix), "]"))
	if err != nil {
		return pgtype.UUID{}, "", nil, "", err
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(parts[1]), &envelope); err != nil {
		return pgtype.UUID{}, "", nil, "", fmt.Errorf("decode structured workflow envelope: %w", err)
	}
	kind, _ := envelope["kind"].(string)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return pgtype.UUID{}, "", nil, "", fmt.Errorf("structured workflow envelope is missing kind")
	}
	return actionID, kind, envelope, marker, nil
}

func (s *TaskService) bindWorkflowContextTask(ctx context.Context, actionID, taskID pgtype.UUID) error {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := projectorchestration.BindWorkflowContextTask(ctx, tx, actionID, taskID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *TaskService) bindDirectContextTask(ctx context.Context, compilationID, taskID pgtype.UUID) error {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := projectorchestration.BindDirectContextTask(ctx, tx, compilationID, taskID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *TaskService) discardDirectContextCompilation(ctx context.Context, compilationID pgtype.UUID) error {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := projectorchestration.DiscardUnboundDirectContext(ctx, tx, compilationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
