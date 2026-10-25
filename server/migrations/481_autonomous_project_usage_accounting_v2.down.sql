DROP INDEX IF EXISTS idx_autonomous_project_usage_accounting_plane_category;

ALTER TABLE autonomous_project_usage_accounting
    DROP CONSTRAINT IF EXISTS autonomous_project_usage_plane,
    DROP CONSTRAINT IF EXISTS autonomous_project_usage_category_format,
    DROP COLUMN IF EXISTS accounting_version,
    DROP COLUMN IF EXISTS brain_context_estimated,
    DROP COLUMN IF EXISTS brain_context_tokens,
    DROP COLUMN IF EXISTS cost_complete,
    DROP COLUMN IF EXISTS cost_usd_ticks,
    DROP COLUMN IF EXISTS cache_write_tokens,
    DROP COLUMN IF EXISTS cache_read_tokens,
    DROP COLUMN IF EXISTS output_tokens,
    DROP COLUMN IF EXISTS input_tokens,
    DROP COLUMN IF EXISTS plane,
    DROP COLUMN IF EXISTS category;
