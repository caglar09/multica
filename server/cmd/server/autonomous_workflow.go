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
)

// startAutonomousWorkflow attaches to the router-owned TaskService. Team
// planning is executed through a hidden carrier that inherits Mika's selected
// daemon runtime/provider/model; it does not require server-side MULTICA_LLM_*
// credentials.
func startAutonomousWorkflow(
	ctx context.Context,
	bus *events.Bus,
	pool *pgxpool.Pool,
	taskSvc *service.TaskService,
) *workflowruntime.Runtime {
	required := true
	requiredRaw := strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_TEAM_RUNTIME_REQUIRED"))
	if requiredRaw == "" {
		// Backwards compatibility for deployments that already opted into the
		// pre-runtime-planner flag name.
		requiredRaw = strings.TrimSpace(os.Getenv("MULTICA_AUTONOMOUS_TEAM_LLM_REQUIRED"))
	}
	if requiredRaw != "" {
		if value, err := strconv.ParseBool(requiredRaw); err == nil {
			required = value
		} else {
			slog.Warn("invalid autonomous team runtime required flag; using true", "value", requiredRaw)
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

	slog.Info("autonomous team planner configured",
		"transport", "mika_runtime",
		"required", required,
		"max_agents", maxAgents,
	)

	planner := teamprovision.NewRuntimeBackedPlanner(
		workflowruntime.NewMikaTeamPlanExecutor(pool, taskSvc),
		teamprovision.RuntimeBackedPlannerConfig{
			MaxAgents: maxAgents,
			Required:  required,
			Fallback:  teamprovision.NewHeuristicPlanner(),
		},
	)

	runtime, err := workflowruntime.RegisterWithPlanner(
		ctx,
		bus,
		pool,
		taskSvc,
		workflowruntime.ConfigFromEnv(),
		planner,
	)
	if err != nil {
		slog.Error("autonomous workflow failed to start", "error", err)
		return nil
	}
	return runtime
}
