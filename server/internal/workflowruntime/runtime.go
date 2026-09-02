// Package workflowruntime wires the deterministic workflow engine to Multica's
// issue/task domain events and existing TaskService execution path.
package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/teamprovision"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const softwareDevelopmentWorkflow = "software-development"

type Config struct {
	Enabled         bool
	ReviewerAgentID string
	ReviewerRole    string
	MaxReviewCycles int
	PollInterval    time.Duration
	ActionLease     time.Duration
}

func ConfigFromEnv() Config {
	cfg := Config{
		ReviewerAgentID: strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_REVIEWER_AGENT_ID")),
		ReviewerRole:    strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_REVIEWER_ROLE")),
		MaxReviewCycles: 3,
		PollInterval:    500 * time.Millisecond,
		ActionLease:     30 * time.Second,
	}
	if cfg.ReviewerRole == "" {
		cfg.ReviewerRole = "reviewer"
	}
	if raw := strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_WORKFLOW_ENABLED")); raw != "" {
		if enabled, err := strconv.ParseBool(raw); err == nil {
			cfg.Enabled = enabled
		} else {
			slog.Warn("invalid MULTICA_AUTONOMOUS_WORKFLOW_ENABLED; autonomous workflow disabled", "value", raw)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_MAX_REVIEW_CYCLES")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 && value <= 20 {
			cfg.MaxReviewCycles = value
		} else {
			slog.Warn("invalid MULTICA_AUTONOMOUS_MAX_REVIEW_CYCLES; using default", "value", raw, "default", cfg.MaxReviewCycles)
		}
	}
	return cfg
}

type Runtime struct {
	pool    *pgxpool.Pool
	taskSvc *service.TaskService
	store   *workflow.PostgresStore
	engine  *workflow.Engine
	team    *teamprovision.Provisioner
	config  Config
}

func Register(ctx context.Context, bus *events.Bus, pool *pgxpool.Pool, taskSvc *service.TaskService, cfg Config) (*Runtime, error) {
	if !cfg.Enabled {
		slog.Info("autonomous workflow disabled")
		return nil, nil
	}
	if bus == nil || pool == nil || taskSvc == nil {
		return nil, errors.New("autonomous workflow requires event bus, database pool and task service")
	}

	store := workflow.NewPostgresStore(pool)
	engine, err := workflow.New(store, definition())
	if err != nil {
		return nil, fmt.Errorf("build autonomous workflow engine: %w", err)
	}
	r := &Runtime{
		pool: pool,
		taskSvc: taskSvc,
		store: store,
		engine: engine,
		team: teamprovision.New(pool, taskSvc.Queries),
		config: cfg,
	}

	for _, eventType := range []string{protocol.EventIssueCreated, protocol.EventIssueUpdated} {
		bus.Subscribe(eventType, r.onIssueEvent)
	}
	bus.Subscribe(protocol.EventTaskCompleted, r.onTaskCompleted)
	bus.Subscribe(protocol.EventTaskFailed, r.onTaskFailed)
	bus.Subscribe(protocol.EventIssueDeleted, r.onIssueDeleted)
	bus.Subscribe(protocol.EventProjectCreated, r.onProjectCreated)
	bus.Subscribe(protocol.EventProjectDeleted, r.onProjectDeleted)
	bus.Subscribe(protocol.EventWorkspaceDeleted, r.onWorkspaceDeleted)

	worker := workflow.NewActionWorker(store, r, workflow.WorkerOptions{
		PollInterval: cfg.PollInterval,
		Lease:        cfg.ActionLease,
	})
	go worker.Run(ctx)
	go r.runReconciler(ctx)

	slog.Info("autonomous workflow enabled",
		"workflow", softwareDevelopmentWorkflow,
		"reviewer_role", cfg.ReviewerRole,
		"reviewer_agent_id_configured", cfg.ReviewerAgentID != "",
		"max_review_cycles", cfg.MaxReviewCycles,
	)
	return r, nil
}

func definition() workflow.Definition {
	return workflow.Definition{
		Name:         softwareDevelopmentWorkflow,
		Version:      1,
		InitialState: issuestatus.InProgress,
		States: map[string]workflow.State{
			issuestatus.InProgress: {
				OnEnter: []workflow.Action{{
					Type: "trigger_agent",
					Params: map[string]string{
						"selector": "owner",
						"when_state": issuestatus.InProgress,
						"handoff": "Autonomous workflow: implement or revise this issue according to its acceptance criteria and the latest review feedback. Do not manage the workflow or mention/trigger another agent; finish the assigned implementation task normally. The workflow engine routes the next step.",
					},
				}},
			},
			issuestatus.InReview: {
				OnEnter: []workflow.Action{
					{Type: "set_issue_status", Params: map[string]string{"status": issuestatus.InReview, "when_state": issuestatus.InReview}},
					{
						Type: "trigger_agent",
						Params: map[string]string{
							"selector": "reviewer",
							"when_state": issuestatus.InReview,
							"handoff": "Autonomous workflow review: review the implementation against the issue acceptance criteria and project standards. If changes are required, move the issue to In Progress and explain the requested changes in a comment; the workflow engine will automatically return it to the implementation agent. If approved, finish the review task normally; the workflow engine will mark the issue Done. Do not mention or trigger another agent.",
						},
					},
				},
			},
			issuestatus.Done: {
				OnEnter: []workflow.Action{{
					Type: "set_issue_status",
					Params: map[string]string{"status": issuestatus.Done, "when_state": issuestatus.Done},
				}},
			},
			issuestatus.Blocked: {
				OnEnter: []workflow.Action{{
					Type: "set_issue_status",
					Params: map[string]string{"status": issuestatus.Blocked, "when_state": issuestatus.Blocked},
				}},
			},
		},
		Transitions: []workflow.Transition{
			{From: issuestatus.InProgress, Event: "workflow.started", To: issuestatus.InProgress},
			{From: issuestatus.InProgress, Event: "implementation.completed", To: issuestatus.InReview},
			{From: issuestatus.InProgress, Event: "implementation.failed", To: issuestatus.Blocked},
			{From: issuestatus.InReview, Event: "review.completed", To: issuestatus.Done},
			{From: issuestatus.InReview, Event: "review.changes_requested", To: issuestatus.InProgress},
			{From: issuestatus.InReview, Event: "review.exhausted", To: issuestatus.Blocked},
			{From: issuestatus.InReview, Event: "review.failed", To: issuestatus.Blocked},
		},
	}
}

// runReconciler repairs the only gap an in-process event bus cannot close:
// a task/status DB commit can succeed and the API process can die before its
// event is published. Durable runs are therefore periodically compared with
// authoritative issue/task rows and missing terminal events are replayed.
func (r *Runtime) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		if err := r.reconcileOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("autonomous workflow reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) reconcileOnce(ctx context.Context) error {
	runs, err := r.store.ListActiveRuns(ctx, 200)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if err := r.reconcileRun(ctx, run); err != nil && !errors.Is(err, workflow.ErrRevisionConflict) {
			slog.Warn("autonomous workflow run reconciliation failed",
				"run_id", run.ID,
				"issue_id", run.IssueID,
				"state", run.State,
				"error", err,
			)
		}
	}
	return nil
}

func (r *Runtime) reconcileRun(ctx context.Context, run workflow.Run) error {
	issueID, err := util.ParseUUID(run.IssueID)
	if err != nil {
		return err
	}
	issue, err := r.taskSvc.Queries.GetIssue(ctx, issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	effective := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status)

	// A review rejection may have committed while its issue:updated event was
	// lost. The issue row itself is the durable rejection signal.
	if run.State == issuestatus.InReview && effective == issuestatus.InProgress {
		eventType := "review.changes_requested"
		if run.ReviewCycles >= r.config.MaxReviewCycles {
			eventType = "review.exhausted"
		}
		_, err := r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
			ID:                fmt.Sprintf("reconcile-review-change:%s:%d", run.IssueID, issue.Revision),
			Type:              eventType,
			WorkspaceID:       run.WorkspaceID,
			ProjectID:         util.UUIDToString(issue.ProjectID),
			IssueID:           run.IssueID,
			AccountableUserID: run.AccountableUserID,
			Payload:           map[string]any{"status": issue.Status},
		})
		return err
	}

	targetID := run.OwnerAgentID
	if run.State == issuestatus.InReview {
		targetID = run.ReviewerAgentID
	}
	if targetID == "" || run.UpdatedAt.IsZero() {
		return nil
	}
	agentID, err := util.ParseUUID(targetID)
	if err != nil {
		return err
	}

	var terminalTaskID pgtype.UUID
	var terminalStatus string
	err = r.pool.QueryRow(ctx, `
		SELECT id, status
		FROM agent_task_queue
		WHERE issue_id = $1
		  AND agent_id = $2
		  AND status IN ('completed', 'failed')
		  AND COALESCE(completed_at, created_at) > $3
		ORDER BY COALESCE(completed_at, created_at) DESC, created_at DESC, id DESC
		LIMIT 1
	`, issueID, agentID, run.UpdatedAt).Scan(&terminalTaskID, &terminalStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	if terminalStatus == "failed" {
		var active bool
		if err := r.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM agent_task_queue
				WHERE issue_id = $1
				  AND agent_id = $2
				  AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
			)
		`, issueID, agentID).Scan(&active); err != nil {
			return err
		}
		if active {
			return nil
		}
		return r.handleTaskFailed(ctx, events.Event{
			Type:        protocol.EventTaskFailed,
			WorkspaceID: run.WorkspaceID,
			TaskID:      util.UUIDToString(terminalTaskID),
			Payload:     map[string]any{"retry_pending": false},
		})
	}

	return r.handleTaskCompleted(ctx, events.Event{
		Type:        protocol.EventTaskCompleted,
		WorkspaceID: run.WorkspaceID,
		TaskID:      util.UUIDToString(terminalTaskID),
	})
}

func (r *Runtime) onProjectCreated(event events.Event) {
	if r.team == nil || event.WorkspaceID == "" || event.ActorType != "agent" || event.ActorID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workspaceID, err := util.ParseUUID(event.WorkspaceID)
	if err != nil {
		return
	}
	actorID, err := util.ParseUUID(event.ActorID)
	if err != nil || !r.team.IsMikaAgent(ctx, workspaceID, actorID) {
		return
	}
	projectIDValue := projectIDFromEvent(event)
	if projectIDValue == "" {
		return
	}
	projectID, err := util.ParseUUID(projectIDValue)
	if err != nil {
		return
	}
	team, err := r.team.EnsureProject(ctx, workspaceID, projectID)
	if err != nil {
		if errors.Is(err, teamprovision.ErrMikaUnavailable) {
			return
		}
		slog.Warn("autonomous project team provisioning failed",
			"project_id", projectIDValue,
			"workspace_id", event.WorkspaceID,
			"error", err,
		)
		return
	}
	slog.Info("autonomous project team ready",
		"project_id", projectIDValue,
		"workspace_id", event.WorkspaceID,
		"squad_id", util.UUIDToString(team.SquadID),
		"intent", team.Intent,
		"member_count", len(team.Members),
	)
}

func (r *Runtime) onProjectDeleted(event events.Event) {
	if r.team == nil || event.WorkspaceID == "" {
		return
	}
	projectIDValue := projectIDFromEvent(event)
	if projectIDValue == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	workspaceID, err := util.ParseUUID(event.WorkspaceID)
	if err != nil {
		return
	}
	projectID, err := util.ParseUUID(projectIDValue)
	if err != nil {
		return
	}
	if err := r.team.ArchiveProject(ctx, workspaceID, projectID); err != nil {
		slog.Warn("autonomous project team archive failed",
			"project_id", projectIDValue,
			"workspace_id", event.WorkspaceID,
			"error", err,
		)
	}
}

func (r *Runtime) onIssueDeleted(event events.Event) {
	payload, _ := event.Payload.(map[string]any)
	issueID, _ := payload["issue_id"].(string)
	if issueID == "" || event.WorkspaceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.cleanupWorkflowIssue(ctx, event.WorkspaceID, issueID); err != nil {
		slog.Warn("autonomous workflow issue cleanup failed", "issue_id", issueID, "error", err)
	}
}

func (r *Runtime) onWorkspaceDeleted(event events.Event) {
	if event.WorkspaceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	workspaceID, err := util.ParseUUID(event.WorkspaceID)
	if err != nil {
		return
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		slog.Warn("autonomous workflow workspace cleanup begin failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		DELETE FROM autonomous_workflow_action
		WHERE run_id IN (SELECT id FROM autonomous_workflow_run WHERE workspace_id = $1)
	`, workspaceID); err == nil {
		_, err = tx.Exec(ctx, `
			DELETE FROM autonomous_workflow_processed_event
			WHERE run_id IN (SELECT id FROM autonomous_workflow_run WHERE workspace_id = $1)
		`, workspaceID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM autonomous_workflow_run WHERE workspace_id = $1`, workspaceID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `
			DELETE FROM autonomous_project_team_member
			WHERE team_id IN (
				SELECT id FROM autonomous_project_team WHERE workspace_id = $1
			)
		`, workspaceID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM autonomous_project_team WHERE workspace_id = $1`, workspaceID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		slog.Warn("autonomous workflow workspace cleanup failed", "workspace_id", event.WorkspaceID, "error", err)
	}
}

func (r *Runtime) cleanupWorkflowIssue(ctx context.Context, workspaceIDValue, issueIDValue string) error {
	workspaceID, err := util.ParseUUID(workspaceIDValue)
	if err != nil {
		return err
	}
	issueID, err := util.ParseUUID(issueIDValue)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `
		DELETE FROM autonomous_workflow_action
		WHERE run_id IN (
			SELECT id FROM autonomous_workflow_run
			WHERE workspace_id = $1 AND issue_id = $2
		)
	`, workspaceID, issueID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM autonomous_workflow_processed_event
		WHERE run_id IN (
			SELECT id FROM autonomous_workflow_run
			WHERE workspace_id = $1 AND issue_id = $2
		)
	`, workspaceID, issueID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM autonomous_workflow_run
		WHERE workspace_id = $1 AND issue_id = $2
	`, workspaceID, issueID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Runtime) onIssueEvent(event events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.handleIssueEvent(ctx, event); err != nil && !errors.Is(err, workflow.ErrRevisionConflict) {
		slog.Warn("autonomous workflow issue event failed",
			"event_type", event.Type,
			"workspace_id", event.WorkspaceID,
			"error", err,
		)
	}
}

func (r *Runtime) handleIssueEvent(ctx context.Context, event events.Event) error {
	snapshot, prevStatus, err := issueSnapshotFromEvent(event)
	if err != nil || snapshot.ID == "" {
		return err
	}
	issueID, err := util.ParseUUID(snapshot.ID)
	if err != nil {
		return err
	}
	issue, err := r.taskSvc.Queries.GetIssue(ctx, issueID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	effective := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status)
	if effective != issuestatus.InProgress {
		return nil
	}

	run, exists, err := r.store.FindRun(ctx, softwareDevelopmentWorkflow, event.WorkspaceID, snapshot.ID)
	if err != nil {
		return err
	}
	if exists {
		if run.State != issuestatus.InReview {
			return nil
		}
		if prevStatus == "" {
			return nil
		}
		prevEffective := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, prevStatus)
		if prevEffective != issuestatus.InReview {
			return nil
		}
		eventType := "review.changes_requested"
		if run.ReviewCycles >= r.config.MaxReviewCycles {
			eventType = "review.exhausted"
		}
		_, err = r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
			ID:                issueEventID("review-change", snapshot),
			Type:              eventType,
			WorkspaceID:       event.WorkspaceID,
			ProjectID:         util.UUIDToString(issue.ProjectID),
			IssueID:           snapshot.ID,
			ActorType:         event.ActorType,
			ActorID:           event.ActorID,
			AccountableUserID: accountableFromEvent(event),
			Payload:           map[string]any{"status": issue.Status, "previous_status": prevStatus},
		})
		return err
	}

	ownerID, reviewerID, err := r.resolveTeam(ctx, issue, pgtype.UUID{})
	if err != nil {
		slog.Info("autonomous workflow not started: team could not be resolved",
			"issue_id", snapshot.ID,
			"error", err,
		)
		return nil
	}
	_, err = r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
		ID:                issueEventID("workflow-start", snapshot),
		Type:              "workflow.started",
		WorkspaceID:       event.WorkspaceID,
		ProjectID:         util.UUIDToString(issue.ProjectID),
		IssueID:           snapshot.ID,
		ActorType:         event.ActorType,
		ActorID:           event.ActorID,
		OwnerAgentID:      util.UUIDToString(ownerID),
		ReviewerAgentID:   util.UUIDToString(reviewerID),
		AccountableUserID: accountableFromEvent(event),
		Payload:           map[string]any{"status": issue.Status},
	})
	return err
}

func (r *Runtime) onTaskCompleted(event events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.handleTaskCompleted(ctx, event); err != nil && !errors.Is(err, workflow.ErrRevisionConflict) {
		slog.Warn("autonomous workflow task completion failed",
			"task_id", event.TaskID,
			"workspace_id", event.WorkspaceID,
			"error", err,
		)
	}
}

func (r *Runtime) handleTaskCompleted(ctx context.Context, event events.Event) error {
	task, issue, err := r.loadIssueTask(ctx, event)
	if err != nil || !task.ID.Valid {
		return err
	}

	issueID := util.UUIDToString(issue.ID)
	run, exists, err := r.store.FindRun(ctx, softwareDevelopmentWorkflow, event.WorkspaceID, issueID)
	if err != nil {
		return err
	}

	if !exists {
		effective := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status)
		if effective != issuestatus.InProgress && effective != issuestatus.InReview {
			return nil
		}
		resolvedOwnerID, reviewerID, resolveErr := r.resolveTeam(ctx, issue, task.AgentID)
		if resolveErr != nil {
			slog.Info("autonomous workflow not started from completion: team unavailable",
				"issue_id", issueID,
				"agent_id", util.UUIDToString(task.AgentID),
				"error", resolveErr,
			)
			return nil
		}

		eventType := "implementation.completed"
		eventID := "implementation-completed:" + util.UUIDToString(task.ID)
		if resolvedOwnerID != task.AgentID {
			// A Mika-owned legacy/manual task completed before the technology
			// team took ownership. Start the deterministic workflow instead of
			// treating Mika's coordination run as implementation evidence.
			eventType = "workflow.started"
			eventID = "workflow-start-after-coordinator:" + util.UUIDToString(task.ID)
		}
		_, err = r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
			ID:                eventID,
			Type:              eventType,
			WorkspaceID:       event.WorkspaceID,
			ProjectID:         util.UUIDToString(issue.ProjectID),
			IssueID:           issueID,
			OwnerAgentID:      util.UUIDToString(resolvedOwnerID),
			ReviewerAgentID:   util.UUIDToString(reviewerID),
			AccountableUserID: util.UUIDToString(task.AccountableUserID),
			Payload:           map[string]any{"task_id": util.UUIDToString(task.ID)},
		})
		return err
	}

	switch {
	case run.State == issuestatus.InProgress && run.OwnerAgentID == util.UUIDToString(task.AgentID):
		_, err = r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
			ID:                "implementation-completed:" + util.UUIDToString(task.ID),
			Type:              "implementation.completed",
			WorkspaceID:       event.WorkspaceID,
			ProjectID:         util.UUIDToString(issue.ProjectID),
			IssueID:           issueID,
			AccountableUserID: util.UUIDToString(task.AccountableUserID),
			Payload:           map[string]any{"task_id": util.UUIDToString(task.ID)},
		})
		return err

	case run.State == issuestatus.InReview && run.ReviewerAgentID == util.UUIDToString(task.AgentID):
		effective := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status)
		if effective == issuestatus.InProgress {
			eventType := "review.changes_requested"
			if run.ReviewCycles >= r.config.MaxReviewCycles {
				eventType = "review.exhausted"
			}
			_, err = r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
				ID:                "review-change-completed:" + util.UUIDToString(task.ID),
				Type:              eventType,
				WorkspaceID:       event.WorkspaceID,
				ProjectID:         util.UUIDToString(issue.ProjectID),
				IssueID:           issueID,
				AccountableUserID: util.UUIDToString(task.AccountableUserID),
				Payload:           map[string]any{"task_id": util.UUIDToString(task.ID)},
			})
			return err
		}
		if effective != issuestatus.InReview && effective != issuestatus.Done {
			return nil
		}
		_, err = r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
			ID:                "review-completed:" + util.UUIDToString(task.ID),
			Type:              "review.completed",
			WorkspaceID:       event.WorkspaceID,
			ProjectID:         util.UUIDToString(issue.ProjectID),
			IssueID:           issueID,
			AccountableUserID: util.UUIDToString(task.AccountableUserID),
			Payload:           map[string]any{"task_id": util.UUIDToString(task.ID)},
		})
		return err
	}
	return nil
}

func (r *Runtime) onTaskFailed(event events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if retryPending(event.Payload) {
		return
	}
	if err := r.handleTaskFailed(ctx, event); err != nil && !errors.Is(err, workflow.ErrRevisionConflict) {
		slog.Warn("autonomous workflow task failure failed",
			"task_id", event.TaskID,
			"workspace_id", event.WorkspaceID,
			"error", err,
		)
	}
}

func (r *Runtime) handleTaskFailed(ctx context.Context, event events.Event) error {
	task, issue, err := r.loadIssueTask(ctx, event)
	if err != nil || !task.ID.Valid {
		return err
	}
	run, exists, err := r.store.FindRun(ctx, softwareDevelopmentWorkflow, event.WorkspaceID, util.UUIDToString(issue.ID))
	if err != nil || !exists {
		return err
	}

	eventType := ""
	switch {
	case run.State == issuestatus.InProgress && run.OwnerAgentID == util.UUIDToString(task.AgentID):
		eventType = "implementation.failed"
	case run.State == issuestatus.InReview && run.ReviewerAgentID == util.UUIDToString(task.AgentID):
		eventType = "review.failed"
	default:
		return nil
	}
	_, err = r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
		ID:                eventType + ":" + util.UUIDToString(task.ID),
		Type:              eventType,
		WorkspaceID:       event.WorkspaceID,
		ProjectID:         util.UUIDToString(issue.ProjectID),
		IssueID:           util.UUIDToString(issue.ID),
		AccountableUserID: util.UUIDToString(task.AccountableUserID),
		Payload:           map[string]any{"task_id": util.UUIDToString(task.ID)},
	})
	return err
}

func (r *Runtime) loadIssueTask(ctx context.Context, event events.Event) (db.AgentTaskQueue, db.Issue, error) {
	taskID := event.TaskID
	if taskID == "" {
		if payload, ok := event.Payload.(map[string]any); ok {
			taskID, _ = payload["task_id"].(string)
		}
	}
	if taskID == "" {
		return db.AgentTaskQueue{}, db.Issue{}, nil
	}
	id, err := util.ParseUUID(taskID)
	if err != nil {
		return db.AgentTaskQueue{}, db.Issue{}, err
	}
	task, err := r.taskSvc.Queries.GetAgentTask(ctx, id)
	if err != nil {
		return db.AgentTaskQueue{}, db.Issue{}, err
	}
	if !task.IssueID.Valid || task.ChatSessionID.Valid || task.AutopilotRunID.Valid {
		return db.AgentTaskQueue{}, db.Issue{}, nil
	}
	issue, err := r.taskSvc.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		return db.AgentTaskQueue{}, db.Issue{}, err
	}
	return task, issue, nil
}

func (r *Runtime) resolveTeam(ctx context.Context, issue db.Issue, ownerHint pgtype.UUID) (pgtype.UUID, pgtype.UUID, error) {
	ownerID := ownerHint
	preferredSquad := pgtype.UUID{}

	if !ownerID.Valid {
		switch {
		case issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid:
			ownerID = issue.AssigneeID
		case issue.AssigneeType.Valid && issue.AssigneeType.String == "squad" && issue.AssigneeID.Valid:
			preferredSquad = issue.AssigneeID
			if err := r.pool.QueryRow(ctx, `
				SELECT leader_id
				FROM squad
				WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL
			`, issue.AssigneeID, issue.WorkspaceID).Scan(&ownerID); err != nil {
				return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("resolve squad leader: %w", err)
			}
		}
	}

	// Project-team routing becomes authoritative when no implementation owner
	// exists yet or the current owner is Mika. Mika is the Chief of Staff: she
	// bootstraps and coordinates the technology team, but does not become the
	// implementation/review quality gate herself.
	if issue.ProjectID.Valid && r.team != nil {
		provisionedOwner := !ownerID.Valid || r.team.IsMikaAgent(ctx, issue.WorkspaceID, ownerID)
		if provisionedOwner {
			implementationID, team, err := r.team.ImplementationAgent(ctx, issue)
			if err == nil {
				reviewerID, ok := team.Agent(teamprovision.RoleCodeReviewer)
				if !ok {
					return pgtype.UUID{}, pgtype.UUID{}, errors.New("autonomous project team has no code reviewer")
				}
				if implementationID == reviewerID {
					return pgtype.UUID{}, pgtype.UUID{}, errors.New("autonomous project implementation and review agents must differ")
				}
				return implementationID, reviewerID, nil
			}
			if !ownerID.Valid {
				return pgtype.UUID{}, pgtype.UUID{}, err
			}
			slog.Info("autonomous project team unavailable; falling back to existing assignee routing",
				"issue_id", util.UUIDToString(issue.ID),
				"project_id", util.UUIDToString(issue.ProjectID),
				"error", err,
			)
		} else if team, ok, err := r.team.FindProject(ctx, issue.WorkspaceID, issue.ProjectID); err == nil && ok {
			if reviewerID, exists := team.Agent(teamprovision.RoleCodeReviewer); exists && reviewerID != ownerID {
				return ownerID, reviewerID, nil
			}
		}
	}

	if !ownerID.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, errors.New("workflow owner agent is missing")
	}

	reviewerID, err := r.resolveReviewer(ctx, issue.WorkspaceID, ownerID, preferredSquad)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	return ownerID, reviewerID, nil
}

func (r *Runtime) resolveReviewer(ctx context.Context, workspaceID, ownerID, preferredSquad pgtype.UUID) (pgtype.UUID, error) {
	if r.config.ReviewerAgentID != "" {
		configured, err := util.ParseUUID(r.config.ReviewerAgentID)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("parse configured reviewer agent: %w", err)
		}
		var reviewer pgtype.UUID
		err = r.pool.QueryRow(ctx, `
			SELECT id
			FROM agent
			WHERE id = $1
			  AND workspace_id = $2
			  AND archived_at IS NULL
			  AND kind = 'user'
			  AND runtime_id IS NOT NULL
		`, configured, workspaceID).Scan(&reviewer)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("configured reviewer is not an active runnable agent in this workspace: %w", err)
		}
		if reviewer == ownerID {
			return pgtype.UUID{}, errors.New("configured reviewer must be different from workflow owner")
		}
		return reviewer, nil
	}

	var reviewer pgtype.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT a.id
		FROM squad_member reviewer_member
		JOIN squad s ON s.id = reviewer_member.squad_id
		JOIN agent a
		  ON reviewer_member.member_type = 'agent'
		 AND a.id = reviewer_member.member_id
		WHERE s.workspace_id = $1
		  AND s.archived_at IS NULL
		  AND lower(trim(reviewer_member.role)) = lower(trim($2))
		  AND a.archived_at IS NULL
		  AND a.kind = 'user'
		  AND a.runtime_id IS NOT NULL
		  AND a.id <> $3
		  AND (
			($4::uuid IS NOT NULL AND s.id = $4)
			OR EXISTS (
				SELECT 1
				FROM squad_member owner_member
				WHERE owner_member.squad_id = s.id
				  AND owner_member.member_type = 'agent'
				  AND owner_member.member_id = $3
			)
		  )
		ORDER BY
		  CASE WHEN $4::uuid IS NOT NULL AND s.id = $4 THEN 0 ELSE 1 END,
		  reviewer_member.created_at ASC,
		  a.created_at ASC
		LIMIT 1
	`, workspaceID, r.config.ReviewerRole, ownerID, nullableUUID(preferredSquad)).Scan(&reviewer)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, fmt.Errorf("no runnable squad agent has role %q; set that squad-member role or MULTICA_AUTONOMOUS_REVIEWER_AGENT_ID", r.config.ReviewerRole)
	}
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("resolve reviewer role: %w", err)
	}
	return reviewer, nil
}

// ExecuteWorkflowAction maps durable actions onto Multica's normal task and
// issue services rather than bypassing their safety gates.
func (r *Runtime) ExecuteWorkflowAction(ctx context.Context, run workflow.Run, pending workflow.PendingAction) error {
	issueID, err := util.ParseUUID(run.IssueID)
	if err != nil {
		return err
	}
	if expected := strings.TrimSpace(pending.Action.Params["when_state"]); expected != "" && run.State != expected {
		// A slow/reclaimed action from an older transition is obsolete once the
		// run has advanced. Treating it as complete prevents a crash window from
		// dispatching yesterday's owner/reviewer task again.
		return nil
	}
	switch pending.Action.Type {
	case "set_issue_status":
		status := strings.TrimSpace(pending.Action.Params["status"])
		if status == "" {
			return errors.New("set_issue_status action is missing status")
		}
		_, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issueID, status)
		return err

	case "trigger_agent":
		selector := strings.TrimSpace(pending.Action.Params["selector"])
		targetID := ""
		switch selector {
		case "owner":
			targetID = run.OwnerAgentID
		case "reviewer":
			targetID = run.ReviewerAgentID
		default:
			return fmt.Errorf("unsupported workflow agent selector %q", selector)
		}
		if targetID == "" {
			return fmt.Errorf("workflow %s agent is not resolved", selector)
		}

		agentID, err := util.ParseUUID(targetID)
		if err != nil {
			return err
		}
		accountableID := pgtype.UUID{}
		if run.AccountableUserID != "" {
			accountableID, err = util.ParseUUID(run.AccountableUserID)
			if err != nil {
				return err
			}
		}
		issue, err := r.taskSvc.Queries.GetIssue(ctx, issueID)
		if err != nil {
			return err
		}

		// Make the side effect idempotent across the narrow crash window between
		// TaskService committing the queued task and this worker marking its
		// durable action completed. The marker lives in handoff_note, which is
		// already task execution metadata and survives terminal task history.
		marker := "[workflow-action:" + pending.ID + "]"
		var alreadyDispatched bool
		if err := r.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM agent_task_queue
				WHERE issue_id = $1
				  AND agent_id = $2
				  AND handoff_note LIKE $3 || '%'
			)
		`, issueID, agentID, marker).Scan(&alreadyDispatched); err != nil {
			return fmt.Errorf("check workflow dispatch receipt: %w", err)
		}
		if alreadyDispatched {
			return nil
		}
		handoff := marker + "\n" + pending.Action.Params["handoff"]
		_, err = r.taskSvc.EnqueueTaskForWorkflow(
			ctx,
			issue,
			agentID,
			accountableID,
			handoff,
		)
		if errors.Is(err, service.ErrDuplicatePendingTask) {
			return nil
		}
		return err
	default:
		return fmt.Errorf("unsupported workflow action type %q", pending.Action.Type)
	}
}

// WorkflowActionExhausted moves the issue to Blocked after the durable action
// retry budget is exhausted. This is intentionally best-effort: the original
// failure is already durably recorded on autonomous_workflow_action.
func (r *Runtime) WorkflowActionExhausted(ctx context.Context, run workflow.Run, pending workflow.PendingAction, cause error) {
	issueID, err := util.ParseUUID(run.IssueID)
	if err != nil {
		return
	}
	if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issueID, issuestatus.Blocked); err != nil {
		slog.Error("workflow exhaustion could not block issue",
			"run_id", run.ID,
			"action_id", pending.ID,
			"cause", cause,
			"error", err,
		)
	}
}

type issueSnapshot struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

func issueSnapshotFromEvent(event events.Event) (issueSnapshot, string, error) {
	payload, ok := event.Payload.(map[string]any)
	if !ok {
		return issueSnapshot{}, "", nil
	}
	raw, err := json.Marshal(payload["issue"])
	if err != nil {
		return issueSnapshot{}, "", err
	}
	var snapshot issueSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return issueSnapshot{}, "", err
	}
	prevStatus, _ := payload["prev_status"].(string)
	return snapshot, prevStatus, nil
}

func projectIDFromEvent(event events.Event) string {
	payload, ok := event.Payload.(map[string]any)
	if !ok {
		return ""
	}
	if id, _ := payload["project_id"].(string); id != "" {
		return id
	}
	raw, err := json.Marshal(payload["project"])
	if err != nil {
		return ""
	}
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &project); err != nil {
		return ""
	}
	return project.ID
}

func issueEventID(prefix string, snapshot issueSnapshot) string {
	return fmt.Sprintf("%s:%s:%d:%s", prefix, snapshot.ID, snapshot.Revision, snapshot.Status)
}

func accountableFromEvent(event events.Event) string {
	if event.ActorType == "member" {
		return event.ActorID
	}
	return ""
}

func retryPending(payload any) bool {
	fields, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	value, _ := fields["retry_pending"].(bool)
	return value
}

func nullableUUID(value pgtype.UUID) any {
	if !value.Valid {
		return nil
	}
	return value
}
