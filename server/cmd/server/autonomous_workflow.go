package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/teamprovision"
	"github.com/multica-ai/multica/server/internal/workflowruntime"
	"github.com/multica-ai/multica/server/pkg/llm"
)

// startAutonomousWorkflow attaches to the router-owned TaskService and the
// router-owned internal LLM client. Constructing parallel copies here would
// bypass daemon wake/cache wiring and the operator's effective LLM retry policy.
func startAutonomousWorkflow(
	ctx context.Context,
	bus *events.Bus,
	pool *pgxpool.Pool,
	taskSvc *service.TaskService,
	llmClient *llm.Client,
) {
	required := true
	if raw := strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_TEAM_LLM_REQUIRED")); raw != "" {
		if value, err := strconv.ParseBool(raw); err == nil {
			required = value
		} else {
			slog.Warn("invalid MULTICA_AUTONOMOUS_TEAM_LLM_REQUIRED; using true", "value", raw)
		}
	}

	maxAgents := 12
	if raw := strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_TEAM_MAX_AGENTS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 2 && value <= 20 {
			maxAgents = value
		} else {
			slog.Warn("invalid MULTICA_AUTONOMOUS_TEAM_MAX_AGENTS; using default", "value", raw, "default", maxAgents)
		}
	}

	planner := teamprovision.NewModelBackedPlanner(llmClient, teamprovision.ModelBackedPlannerConfig{
		Model: strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_TEAM_MODEL")),
		MaxAgents: maxAgents,
		Required: required,
		Fallback: teamprovision.NewHeuristicPlanner(),
	})

	if _, err := workflowruntime.RegisterWithPlanner(
		ctx,
		bus,
		pool,
		taskSvc,
		workflowruntime.ConfigFromEnv(),
		planner,
	); err != nil {
		slog.Error("autonomous workflow failed to start", "error", err)
	}
}
