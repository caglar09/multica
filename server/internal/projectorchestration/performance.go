package projectorchestration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AgentOutcome string

const (
	OutcomeStarted          AgentOutcome = "started"
	OutcomeCompleted        AgentOutcome = "completed"
	OutcomeFailed           AgentOutcome = "failed"
	OutcomeReviewRejected   AgentOutcome = "review_rejected"
	OutcomeRetried          AgentOutcome = "retried"
)

type AgentPerformance struct {
	TasksStarted        int64
	TasksCompleted      int64
	TasksFailed         int64
	ReviewRejections    int64
	Retries             int64
	TotalRuntimeSeconds int64
}

// Score is intentionally simple and deterministic. Queue pressure is applied
// separately by the scheduler; this value captures historical delivery quality.
func (p AgentPerformance) Score() float64 {
	total := p.TasksCompleted + p.TasksFailed
	if total == 0 {
		return 0
	}
	success := float64(p.TasksCompleted) / float64(total)
	penalty := float64(p.TasksFailed+p.ReviewRejections) / float64(total+1)
	return success*100 - penalty*40
}

func (s *Store) RecordAgentOutcome(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
	roleFamily string,
	outcome AgentOutcome,
	runtime time.Duration,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	if !workspaceID.Valid || !agentID.Valid || roleFamily == "" {
		return errors.New("workspace, agent and role family are required")
	}
	started, completed, failed, rejected, retries := int64(0), int64(0), int64(0), int64(0), int64(0)
	switch outcome {
	case OutcomeStarted:
		started = 1
	case OutcomeCompleted:
		completed = 1
	case OutcomeFailed:
		failed = 1
	case OutcomeReviewRejected:
		rejected = 1
	case OutcomeRetried:
		retries = 1
	default:
		return fmt.Errorf("unsupported agent outcome %q", outcome)
	}
	runtimeSeconds := int64(runtime / time.Second)
	if runtimeSeconds < 0 {
		runtimeSeconds = 0
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO autonomous_agent_performance (
			workspace_id, agent_id, role_family, tasks_started, tasks_completed,
			tasks_failed, review_rejections, retries, total_runtime_seconds,
			last_outcome_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
		ON CONFLICT (workspace_id, agent_id, role_family) DO UPDATE
		SET tasks_started = autonomous_agent_performance.tasks_started + EXCLUDED.tasks_started,
		    tasks_completed = autonomous_agent_performance.tasks_completed + EXCLUDED.tasks_completed,
		    tasks_failed = autonomous_agent_performance.tasks_failed + EXCLUDED.tasks_failed,
		    review_rejections = autonomous_agent_performance.review_rejections + EXCLUDED.review_rejections,
		    retries = autonomous_agent_performance.retries + EXCLUDED.retries,
		    total_runtime_seconds = autonomous_agent_performance.total_runtime_seconds + EXCLUDED.total_runtime_seconds,
		    last_outcome_at = now(),
		    updated_at = now()
	`, workspaceID, agentID, roleFamily, started, completed, failed, rejected, retries, runtimeSeconds)
	return err
}

func (s *Store) AgentPerformance(
	ctx context.Context,
	workspaceID, agentID pgtype.UUID,
	roleFamily string,
) (AgentPerformance, error) {
	var out AgentPerformance
	err := s.pool.QueryRow(ctx, `
		SELECT tasks_started, tasks_completed, tasks_failed, review_rejections,
		       retries, total_runtime_seconds
		FROM autonomous_agent_performance
		WHERE workspace_id = $1 AND agent_id = $2 AND role_family = $3
	`, workspaceID, agentID, roleFamily).Scan(
		&out.TasksStarted, &out.TasksCompleted, &out.TasksFailed,
		&out.ReviewRejections, &out.Retries, &out.TotalRuntimeSeconds,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// No history is a neutral candidate.
		return AgentPerformance{}, nil
	}
	if err != nil {
		return AgentPerformance{}, err
	}
	return out, nil
}
