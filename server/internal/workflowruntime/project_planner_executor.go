package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const (
	projectPlannerPollPeriod = 300 * time.Millisecond
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
