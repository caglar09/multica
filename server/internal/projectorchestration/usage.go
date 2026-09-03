package projectorchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	UsageExecution       = "execution"
	UsageReview          = "review"
	UsageQA              = "qa"
	UsageSecurity        = "security"
	UsageIntegration     = "integration"
	UsageTeamPlanning    = "team_planning"
	UsageProjectPlanning = "project_planning"
	UsageBrainLearning   = "brain_learning"
	UsagePlannerRepair   = "planner_repair"
)

const (
	UsagePlaneExecution = "execution"
	UsagePlaneControl   = "control"
)

// UsageAttribution is the immutable accounting payload for one runtime task.
// ProjectID attribution is intentionally independent from chat-session binding:
// hidden control-plane sessions can stay detached from project worktrees while
// their provider usage is still charged to the real project.
type UsageAttribution struct {
	Category              string
	InputTokens           int64
	OutputTokens          int64
	CacheReadTokens       int64
	CacheWriteTokens      int64
	RuntimeSeconds        int64
	CostUsdTicks          int64
	CostComplete          bool
	BrainContextTokens    int64
	BrainContextEstimated bool
}

func (u UsageAttribution) TotalTokens() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

func (u UsageAttribution) CostMicrounits() int64 {
	if u.CostUsdTicks <= 0 {
		return 0
	}
	// task_usage uses 1e-10 USD ticks; Project OS budgets use micro-USD.
	return (u.CostUsdTicks + 9999) / 10000
}

func UsagePlaneForCategory(category string) string {
	switch strings.TrimSpace(category) {
	case UsageExecution, UsageReview, UsageQA, UsageSecurity, UsageIntegration:
		return UsagePlaneExecution
	default:
		return UsagePlaneControl
	}
}

func validateUsageCategory(category string) error {
	category = strings.TrimSpace(category)
	if category == "" || len(category) > 64 {
		return errors.New("project usage category is invalid")
	}
	for i, r := range category {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9' && i > 0) || (r == '_' && i > 0) {
			continue
		}
		return fmt.Errorf("project usage category %q must be lower_snake_case", category)
	}
	return nil
}

// AccountTaskUsageDetailed durably attributes a task to a project and updates
// its budget exactly once. The accounting row is the idempotency receipt.
//
// Budget counters are incremented even when the new totals exceed a configured
// ceiling because the provider cost has already happened. ErrBudgetExceeded is
// returned after commit so orchestration can block subsequent work without
// under-reporting the cost that caused the breach.
func (s *Store) AccountTaskUsageDetailed(
	ctx context.Context,
	workspaceID, projectID, taskID pgtype.UUID,
	usage UsageAttribution,
) error {
	if s == nil || s.pool == nil {
		return errors.New("project orchestration store is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid || !taskID.Valid {
		return nil
	}
	if err := validateUsageCategory(usage.Category); err != nil {
		return err
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadTokens < 0 ||
		usage.CacheWriteTokens < 0 || usage.RuntimeSeconds < 0 || usage.CostUsdTicks < 0 ||
		usage.BrainContextTokens < 0 {
		return errors.New("project usage counters cannot be negative")
	}

	tokens := usage.TotalTokens()
	costMicrounits := usage.CostMicrounits()
	plane := UsagePlaneForCategory(usage.Category)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_usage_accounting (
			task_id, workspace_id, project_id, category, plane,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			tokens, runtime_seconds, cost_usd_ticks, cost_microunits, cost_complete,
			brain_context_tokens, brain_context_estimated, accounting_version
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13, $14,
			$15, $16, 2
		)
		ON CONFLICT (task_id) DO NOTHING
	`, taskID, workspaceID, projectID, usage.Category, plane,
		usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens,
		tokens, usage.RuntimeSeconds, usage.CostUsdTicks, costMicrounits, usage.CostComplete,
		usage.BrainContextTokens, usage.BrainContextEstimated)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	var tokenExceeded, runtimeExceeded, costExceeded bool
	err = tx.QueryRow(ctx, `
		UPDATE autonomous_project_budget
		SET tokens_used = tokens_used + $3,
		    runtime_seconds_used = runtime_seconds_used + $4,
		    cost_microunits_used = cost_microunits_used + $5,
		    updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2
		RETURNING
		    token_limit IS NOT NULL AND tokens_used > token_limit,
		    runtime_seconds_limit IS NOT NULL AND runtime_seconds_used > runtime_seconds_limit,
		    cost_microunits_limit IS NOT NULL AND cost_microunits_used > cost_microunits_limit
	`, workspaceID, projectID, tokens, usage.RuntimeSeconds, costMicrounits).
		Scan(&tokenExceeded, &runtimeExceeded, &costExceeded)
	if errors.Is(err, pgx.ErrNoRows) {
		// Planning can legitimately precede budget creation. upsertBudget seeds
		// the future row from these durable accounting receipts.
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if tokenExceeded || runtimeExceeded || costExceeded {
		return ErrBudgetExceeded
	}
	return nil
}
