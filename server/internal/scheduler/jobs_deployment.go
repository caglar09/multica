package scheduler

import (
	"context"
	"time"
)

// DeploymentReconciler is implemented by the server handler so the scheduler
// stays independent from provider adapters.
type DeploymentReconciler interface {
	ReconcileDeployments(context.Context) (int, error)
}

const JobNameDeploymentOperationReconciliation = "deployment_operation_reconciliation"

// DeploymentOperationReconciliationJob runs the generic provider dispatcher
// and reconciler from the existing durable scheduler. Operation-level leases,
// backoff, and deadlines remain in the deployment repository.
func DeploymentOperationReconciliationJob(worker DeploymentReconciler) JobSpec {
	return JobSpec{
		Name:              JobNameDeploymentOperationReconciliation,
		Cadence:           10 * time.Second,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     5 * time.Minute,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      3 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if worker == nil {
				return HandlerResult{}, nil
			}
			processed, err := worker.ReconcileDeployments(ctx)
			return HandlerResult{RowsAffected: int64(processed)}, err
		},
	}
}
