package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const (
	projectPlannerPollPeriod  = 300 * time.Millisecond
	goalDriftAuditSessionKind = "autonomous_goal_drift_audit"
)

type MikaProjectPlanExecutor struct {
	pool    *pgxpool.Pool
	taskSvc *service.TaskService
}

func NewMikaProjectPlanExecutor(pool *pgxpool.Pool, taskSvc *service.TaskService) *MikaProjectPlanExecutor {
	return &MikaProjectPlanExecutor{pool: pool, taskSvc: taskSvc}
}

func (e *MikaProjectPlanExecutor) ExecuteProjectPlan(
	ctx context.Context,
	input projectorchestration.PlanningInput,
	systemPrompt string,
	userPrompt string,
) (projectorchestration.RuntimeExecution, error) {
	if e == nil || e.pool == nil || e.taskSvc == nil || e.taskSvc.Queries == nil {
		return projectorchestration.RuntimeExecution{}, projectorchestration.ErrPlannerUnavailable
	}
	carrier, runtime, err := e.ensureProjectManagerCarrier(ctx, input.WorkspaceID, input.ProjectID)
	if err != nil {
		return projectorchestration.RuntimeExecution{}, err
	}

	// Planning has a different output contract from the customer conversation.
	// Persist it as project-owned work, never as a standard or leader chat turn.
	var sessionID pgtype.UUID
	err = e.pool.QueryRow(ctx, `
		INSERT INTO chat_session (id, workspace_id, agent_id, creator_id, title, runtime_id, project_id, session_kind)
		VALUES ($1,$2,$3,$4,'Autonomous Project Planning',$5,$6,'autonomous_project_planning')
		RETURNING id
	`, dbid.NewV7(), input.WorkspaceID, carrier.ID, carrier.OwnerID, carrier.RuntimeID, input.ProjectID).Scan(&sessionID)
	if err != nil {
		return projectorchestration.RuntimeExecution{}, fmt.Errorf("create project manager planning session: %w", err)
	}
	session, err := e.taskSvc.Queries.GetChatSession(ctx, sessionID)
	if err != nil {
		return projectorchestration.RuntimeExecution{}, fmt.Errorf("load project manager planning session: %w", err)
	}
	prompt := strings.TrimSpace(systemPrompt) + "\n\n" + strings.TrimSpace(userPrompt) +
		"\n\nDo not call tools. Return exactly one ProjectPlan JSON object."

	sent, err := e.taskSvc.SendDirectChatMessage(
		ctx, session, carrier, carrier.OwnerID, prompt, nil, "member", carrier.OwnerID,
	)
	if err != nil {
		return projectorchestration.RuntimeExecution{}, fmt.Errorf("enqueue project manager planning task: %w", err)
	}
	output, err := e.waitForProjectPlannerTask(ctx, sent.Task.ID)
	if err != nil {
		return projectorchestration.RuntimeExecution{}, err
	}
	category := controlPlaneUsageCategory(projectorchestration.UsageProjectPlanning, userPrompt)
	brainContext := make([]projectorchestration.PlanningContextItem, 0, len(input.Context))
	for _, item := range input.Context {
		if item.Source == "brain" {
			brainContext = append(brainContext, item)
		}
	}
	brainContextTokens := int64(0)
	brainContextEstimated := false
	if len(brainContext) > 0 {
		if rawBrain, marshalErr := json.Marshal(brainContext); marshalErr == nil {
			brainContextTokens = estimateInjectedTokens(string(rawBrain))
			brainContextEstimated = brainContextTokens > 0
		}
	}
	if _, usageErr := accountRuntimeTaskUsage(
		ctx, e.pool, projectorchestration.NewStore(e.pool),
		input.WorkspaceID, input.ProjectID, sent.Task.ID, category,
		brainContextTokens, brainContextEstimated,
	); usageErr != nil && !errors.Is(usageErr, projectorchestration.ErrBudgetExceeded) {
		return projectorchestration.RuntimeExecution{}, fmt.Errorf("account project planner usage: %w", usageErr)
	}

	model := ""
	if carrier.Model.Valid {
		model = strings.TrimSpace(carrier.Model.String)
	}
	return projectorchestration.RuntimeExecution{
		Output:   output,
		Provider: strings.TrimSpace(runtime.Provider),
		Model:    model,
	}, nil
}

func (e *MikaProjectPlanExecutor) ExecuteGoalDriftAudit(
	ctx context.Context,
	request projectorchestration.GoalDriftAuditRequest,
	executionID string,
	attempt int,
) (string, error) {
	if e == nil || e.pool == nil || e.taskSvc == nil || e.taskSvc.Queries == nil {
		return "", projectorchestration.ErrPlannerUnavailable
	}
	workspaceID, err := util.ParseUUID(request.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf("parse goal drift workspace id: %w", err)
	}
	projectID, err := util.ParseUUID(request.ProjectID)
	if err != nil {
		return "", fmt.Errorf("parse goal drift project id: %w", err)
	}
	carrier, _, err := e.ensureProjectManagerCarrier(ctx, workspaceID, projectID)
	if err != nil {
		return "", err
	}
	session, err := e.ensureGoalDriftAuditSession(ctx, request, carrier)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(executionID) == "" {
		executionID = request.AuditID
	}
	if attempt < 1 {
		attempt = 1
	}
	messagePrefix := "goal-drift-audit:" + executionID + ":"
	var taskID pgtype.UUID
	var taskStatus string
	err = e.pool.QueryRow(ctx, `
		SELECT t.id, t.status
		FROM chat_message m
		JOIN agent_task_queue t ON t.id=m.task_id
		WHERE m.chat_session_id=$1 AND m.client_message_id=$2
		LIMIT 1
	`, session.ID, messagePrefix+strconv.Itoa(attempt)).Scan(&taskID, &taskStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("find current goal drift daemon task: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = e.pool.QueryRow(ctx, `
			SELECT t.id, t.status
			FROM chat_message m
			JOIN agent_task_queue t ON t.id=m.task_id
			WHERE m.chat_session_id=$1 AND m.client_message_id LIKE $2
			  AND t.status IN ('queued','deferred','dispatched','running')
			ORDER BY m.created_at DESC
			LIMIT 1
		`, session.ID, messagePrefix+"%").Scan(&taskID, &taskStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("find active goal drift daemon task: %w", err)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		prompt, err := goalDriftAuditPrompt(request)
		if err != nil {
			return "", err
		}
		sent, err := e.taskSvc.SendDirectChatMessageWithClientMessageID(
			ctx, session, carrier, carrier.OwnerID, prompt, nil, "member", carrier.OwnerID,
			messagePrefix+strconv.Itoa(attempt),
		)
		if err != nil {
			return "", fmt.Errorf("enqueue goal drift audit on project manager runtime: %w", err)
		}
		taskID = sent.Task.ID
	}
	return e.waitForProjectPlannerTask(ctx, taskID)
}

func (e *MikaProjectPlanExecutor) ensureGoalDriftAuditSession(
	ctx context.Context,
	request projectorchestration.GoalDriftAuditRequest,
	carrier db.Agent,
) (db.ChatSession, error) {
	workspaceID, err := util.ParseUUID(request.WorkspaceID)
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("parse goal drift workspace id: %w", err)
	}
	projectID, err := util.ParseUUID(request.ProjectID)
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("parse goal drift project id: %w", err)
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("begin goal drift audit session: %w", err)
	}
	defer tx.Rollback(ctx)
	lockKey := "autonomous-goal-drift-audit:" + request.WorkspaceID + ":" + request.ProjectID
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return db.ChatSession{}, fmt.Errorf("lock goal drift audit session: %w", err)
	}
	var sessionID, agentID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, agent_id FROM chat_session
		WHERE workspace_id=$1 AND project_id=$2
		  AND session_kind=$3 AND status='active'
		FOR UPDATE
	`, workspaceID, projectID, goalDriftAuditSessionKind).Scan(&sessionID, &agentID)
	if err == nil && agentID != carrier.ID {
		if _, err := tx.Exec(ctx, `
			UPDATE chat_session SET agent_id=$2, runtime_id=$3, updated_at=now()
			WHERE id=$1
		`, sessionID, carrier.ID, carrier.RuntimeID); err != nil {
			return db.ChatSession{}, fmt.Errorf("rebind goal drift audit session: %w", err)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id, is_agent_intro, project_id, id, session_kind)
			VALUES ($1,$2,$3,'Goal Alignment Review',$4,FALSE,$5,$6,$7)
			RETURNING id
		`, workspaceID, carrier.ID, carrier.OwnerID, carrier.RuntimeID, projectID, dbid.NewV7(), goalDriftAuditSessionKind).Scan(&sessionID)
	}
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("ensure goal drift audit session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.ChatSession{}, err
	}
	session, err := e.taskSvc.Queries.GetChatSession(ctx, sessionID)
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("load goal drift audit session: %w", err)
	}
	return session, nil
}

func (e *MikaProjectPlanExecutor) ensureProjectManagerCarrier(ctx context.Context, workspaceID, projectID pgtype.UUID) (db.Agent, db.AgentRuntime, error) {
	if !workspaceID.Valid || !projectID.Valid {
		return db.Agent{}, db.AgentRuntime{}, errors.New("project manager workspace and project are required")
	}
	var agentID pgtype.UUID
	err := e.pool.QueryRow(ctx, `
		SELECT tm.agent_id
		FROM autonomous_project_team t
		JOIN autonomous_project_team_member tm ON tm.team_id = t.id
		WHERE t.workspace_id = $1 AND t.project_id = $2
		  AND t.status = 'active' AND tm.role = 'product_manager' AND tm.active = TRUE
		LIMIT 1
	`, workspaceID, projectID).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, db.AgentRuntime{}, fmt.Errorf("%w: project manager is not provisioned", projectorchestration.ErrPlannerUnavailable)
	}
	if err != nil {
		return db.Agent{}, db.AgentRuntime{}, fmt.Errorf("load project manager: %w", err)
	}
	carrier, err := e.taskSvc.Queries.GetAgent(ctx, agentID)
	if err != nil || carrier.ArchivedAt.Valid || !carrier.RuntimeID.Valid {
		return db.Agent{}, db.AgentRuntime{}, fmt.Errorf("%w: project manager agent is unavailable", projectorchestration.ErrPlannerUnavailable)
	}
	runtime, err := (service.RuntimeLookup{
		Queries: e.taskSvc.Queries,
		Metrics: e.taskSvc.Metrics,
		Source:  obsmetrics.RuntimeLookupSourceOther,
	}).Get(ctx, carrier.RuntimeID)
	if err != nil {
		return db.Agent{}, db.AgentRuntime{}, fmt.Errorf("load project manager runtime: %w", err)
	}
	if runtime.Status != "online" {
		return db.Agent{}, db.AgentRuntime{}, fmt.Errorf("%w: project manager runtime %q is %s", projectorchestration.ErrPlannerUnavailable, runtime.Name, runtime.Status)
	}
	return carrier, runtime, nil
}

func (e *MikaProjectPlanExecutor) waitForProjectPlannerTask(ctx context.Context, taskID pgtype.UUID) (string, error) {
	ticker := time.NewTicker(projectPlannerPollPeriod)
	defer ticker.Stop()
	for {
		task, err := e.taskSvc.Queries.GetAgentTask(ctx, taskID)
		if err != nil {
			return "", fmt.Errorf("read project planner task: %w", err)
		}
		switch task.Status {
		case "completed":
			var result struct {
				Output string `json:"output"`
			}
			if err := json.Unmarshal(task.Result, &result); err != nil {
				return "", fmt.Errorf("decode project planner task result: %w", err)
			}
			if strings.TrimSpace(result.Output) == "" {
				return "", errors.New("project planner runtime returned empty output")
			}
			return result.Output, nil
		case "failed":
			message := "project planner runtime task failed"
			if task.Error.Valid && strings.TrimSpace(task.Error.String) != "" {
				message += ": " + strings.TrimSpace(task.Error.String)
			}
			return "", errors.New(message)
		case "cancelled", "canceled":
			return "", errors.New("project planner runtime task was cancelled")
		}
		select {
		case <-ctx.Done():
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = e.taskSvc.CancelTask(cleanupCtx, taskID)
			cancel()
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

var _ projectorchestration.RuntimePlanExecutor = (*MikaProjectPlanExecutor)(nil)
