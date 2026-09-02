package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/workflowruntime"
)

// startAutonomousWorkflow attaches to the router-owned TaskService. Constructing
// another TaskService here would bypass the daemon wakeup/cache wiring that
// NewRouterWithOptions installed, so this helper only composes existing
// production dependencies.
func startAutonomousWorkflow(ctx context.Context, bus *events.Bus, pool *pgxpool.Pool, taskSvc *service.TaskService) {
	if _, err := workflowruntime.Register(ctx, bus, pool, taskSvc, workflowruntime.ConfigFromEnv()); err != nil {
		slog.Error("autonomous workflow failed to start", "error", err)
	}
}
