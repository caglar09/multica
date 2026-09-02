package workflow

import (
	"context"
	"log/slog"
	"time"
)

// ActionExecutor maps durable workflow actions onto application side effects.
type ActionExecutor interface {
	ExecuteWorkflowAction(ctx context.Context, run Run, action PendingAction) error
}

// ActionExhaustedHandler is optional. Runtimes that implement it can move a
// workflow into a safe human-visible state after the durable retry budget is
// exhausted instead of leaving an issue looking healthy while orchestration is
// permanently stuck.
type ActionExhaustedHandler interface {
	WorkflowActionExhausted(ctx context.Context, run Run, action PendingAction, cause error)
}

type WorkerOptions struct {
	PollInterval time.Duration
	Lease        time.Duration
}

type ActionWorker struct {
	store    *PostgresStore
	executor ActionExecutor
	options  WorkerOptions
}

func NewActionWorker(store *PostgresStore, executor ActionExecutor, options WorkerOptions) *ActionWorker {
	if options.PollInterval <= 0 {
		options.PollInterval = 500 * time.Millisecond
	}
	if options.Lease <= 0 {
		options.Lease = 30 * time.Second
	}
	return &ActionWorker{store: store, executor: executor, options: options}
}

// Run drains durable actions until ctx is cancelled. PostgreSQL leases make the
// loop safe across process crashes and multiple API replicas.
func (w *ActionWorker) Run(ctx context.Context) {
	if w == nil || w.store == nil || w.executor == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		worked := false
		for {
			action, err := w.store.ClaimPendingAction(ctx, w.options.Lease)
			if err != nil {
				slog.Error("workflow action claim failed", "error", err)
				break
			}
			if action == nil {
				break
			}
			worked = true

			run, err := w.store.GetRun(ctx, action.RunID)
			if err == nil {
				err = w.executor.ExecuteWorkflowAction(ctx, run, *action)
			}
			if err != nil {
				exhausted := action.Attempts >= action.MaxAttempts
				if failErr := w.store.FailAction(ctx, *action, err); failErr != nil {
					slog.Error("workflow action retry bookkeeping failed",
						"action_id", action.ID,
						"error", failErr,
					)
				}
				slog.Warn("workflow action failed",
					"action_id", action.ID,
					"run_id", action.RunID,
					"action_type", action.Action.Type,
					"attempt", action.Attempts,
					"max_attempts", action.MaxAttempts,
					"error", err,
				)
				if exhausted {
					if handler, ok := w.executor.(ActionExhaustedHandler); ok {
						handler.WorkflowActionExhausted(ctx, run, *action, err)
					}
				}
				continue
			}
			if err := w.store.CompleteAction(ctx, action.ID, action.LeaseToken); err != nil {
				slog.Warn("workflow action completion lost lease",
					"action_id", action.ID,
					"error", err,
				)
			}
		}

		delay := w.options.PollInterval
		if worked {
			delay = 10 * time.Millisecond
		}
		timer.Reset(delay)
	}
}
