-- Enforce exactly one automatic retry child per source attempt.
--
-- retry_of_task_id is automatic-retry lineage. Manual operator reruns use
-- rerun_of_task_id and are intentionally unaffected. Older builds could create
-- several deferred children for the same parent because deferred rows did not
-- occupy the issue/agent pending slot. Preserve the earliest child as the
-- canonical retry and retain the duplicate lineage in context for audit before
-- clearing the conflicting column.
WITH ranked AS (
    SELECT
        id,
        retry_of_task_id,
        row_number() OVER (
            PARTITION BY retry_of_task_id
            ORDER BY created_at ASC, id ASC
        ) AS rn
    FROM agent_task_queue
    WHERE retry_of_task_id IS NOT NULL
),
duplicates AS (
    SELECT id, retry_of_task_id
    FROM ranked
    WHERE rn > 1
)
UPDATE agent_task_queue t
SET context = COALESCE(t.context, '{}'::jsonb) ||
              jsonb_build_object(
                  'deduplicated_retry_of_task_id',
                  t.retry_of_task_id::text
              ),
    retry_of_task_id = NULL
FROM duplicates d
WHERE t.id = d.id;

CREATE UNIQUE INDEX idx_agent_task_queue_retry_of_task_unique
    ON agent_task_queue (retry_of_task_id)
    WHERE retry_of_task_id IS NOT NULL;
