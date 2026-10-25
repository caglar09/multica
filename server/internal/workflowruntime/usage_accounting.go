package workflowruntime

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

type taskUsageSnapshot struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	RuntimeSeconds   int64
	CostUsdTicks     int64
	CostComplete     bool
}

func (u taskUsageSnapshot) TotalTokens() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

func (u taskUsageSnapshot) CostMicrounits() int64 {
	if u.CostUsdTicks <= 0 {
		return 0
	}
	return (u.CostUsdTicks + 9999) / 10000
}

func loadTaskUsageSnapshot(ctx context.Context, pool *pgxpool.Pool, taskID pgtype.UUID) (taskUsageSnapshot, error) {
	var out taskUsageSnapshot
	var usageRows, costRows int64
	if err := pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(input_tokens), 0)::bigint,
			COALESCE(SUM(output_tokens), 0)::bigint,
			COALESCE(SUM(cache_read_tokens), 0)::bigint,
			COALESCE(SUM(cache_write_tokens), 0)::bigint,
			COALESCE(SUM(cost_usd_ticks), 0)::bigint,
			COUNT(*)::bigint,
			COUNT(cost_usd_ticks)::bigint
		FROM task_usage
		WHERE task_id = $1
	`, taskID).Scan(
		&out.InputTokens,
		&out.OutputTokens,
		&out.CacheReadTokens,
		&out.CacheWriteTokens,
		&out.CostUsdTicks,
		&usageRows,
		&costRows,
	); err != nil {
		return taskUsageSnapshot{}, err
	}
	out.CostComplete = usageRows > 0 && usageRows == costRows

	if err := pool.QueryRow(ctx, `
		SELECT CASE
			WHEN started_at IS NOT NULL AND completed_at IS NOT NULL AND completed_at > started_at
				THEN GREATEST(EXTRACT(EPOCH FROM (completed_at - started_at))::bigint, 0)
			ELSE 0
		END
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&out.RuntimeSeconds); err != nil {
		return taskUsageSnapshot{}, err
	}
	return out, nil
}

func accountRuntimeTaskUsage(
	ctx context.Context,
	pool *pgxpool.Pool,
	store *projectorchestration.Store,
	workspaceID, projectID, taskID pgtype.UUID,
	category string,
	brainContextTokens int64,
	brainContextEstimated bool,
) (taskUsageSnapshot, error) {
	if store == nil || !workspaceID.Valid || !projectID.Valid || !taskID.Valid {
		return taskUsageSnapshot{}, nil
	}
	usage, err := loadTaskUsageSnapshot(ctx, pool, taskID)
	if err != nil {
		return taskUsageSnapshot{}, err
	}
	err = store.AccountTaskUsageDetailed(ctx, workspaceID, projectID, taskID, projectorchestration.UsageAttribution{
		Category:              category,
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		CacheWriteTokens:      usage.CacheWriteTokens,
		RuntimeSeconds:        usage.RuntimeSeconds,
		CostUsdTicks:          usage.CostUsdTicks,
		CostComplete:          usage.CostComplete,
		BrainContextTokens:    brainContextTokens,
		BrainContextEstimated: brainContextEstimated,
	})
	return usage, err
}

func usageCategoryForNodeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "review":
		return projectorchestration.UsageReview
	case "qa":
		return projectorchestration.UsageQA
	case "security":
		return projectorchestration.UsageSecurity
	case "integration":
		return projectorchestration.UsageIntegration
	default:
		return projectorchestration.UsageExecution
	}
}

func controlPlaneUsageCategory(baseCategory, prompt string) string {
	value := strings.ToLower(prompt)
	if strings.Contains(value, "previous answer failed validation") ||
		strings.Contains(value, "previous projectplan was rejected") ||
		strings.Contains(value, "previous json failed validation") {
		return projectorchestration.UsagePlannerRepair
	}
	return baseCategory
}

func ignoreAccountedBudgetError(err error) error {
	if errors.Is(err, projectorchestration.ErrBudgetExceeded) {
		return nil
	}
	return err
}
