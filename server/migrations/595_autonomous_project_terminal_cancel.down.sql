UPDATE autonomous_project_control
SET paused = TRUE,
    paused_at = COALESCE(paused_at, cancelled_at),
    paused_by = COALESCE(paused_by, cancelled_by),
    updated_at = now()
WHERE cancelled_at IS NOT NULL;

ALTER TABLE autonomous_project_control
    DROP COLUMN IF EXISTS cancelled_by,
    DROP COLUMN IF EXISTS cancelled_at;
