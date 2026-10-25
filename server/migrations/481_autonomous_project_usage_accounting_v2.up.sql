-- Phase 1: project-attributed usage accounting.
--
-- Chat/session ProjectID remains an execution-binding concern. This table is
-- the independent accounting attribution boundary, including hidden control
-- plane tasks whose sessions intentionally have no project binding.
ALTER TABLE autonomous_project_usage_accounting
    ADD COLUMN category TEXT NOT NULL DEFAULT 'execution',
    ADD COLUMN plane TEXT NOT NULL DEFAULT 'execution',
    ADD COLUMN input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    ADD COLUMN output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    ADD COLUMN cache_read_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    ADD COLUMN cost_usd_ticks BIGINT NOT NULL DEFAULT 0 CHECK (cost_usd_ticks >= 0),
    ADD COLUMN cost_complete BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN brain_context_tokens BIGINT NOT NULL DEFAULT 0 CHECK (brain_context_tokens >= 0),
    ADD COLUMN brain_context_estimated BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN accounting_version SMALLINT NOT NULL DEFAULT 1 CHECK (accounting_version >= 1),
    ADD CONSTRAINT autonomous_project_usage_category_format
        CHECK (category ~ '^[a-z][a-z0-9_]{0,63}$'),
    ADD CONSTRAINT autonomous_project_usage_plane
        CHECK (plane IN ('execution', 'control'));

-- Existing rows predate per-counter attribution. Keep their authoritative
-- legacy total in tokens and mark version 1 instead of fabricating a split.
ALTER TABLE autonomous_project_usage_accounting
    ALTER COLUMN accounting_version SET DEFAULT 2;

CREATE INDEX IF NOT EXISTS idx_autonomous_project_usage_accounting_plane_category
    ON autonomous_project_usage_accounting(workspace_id, project_id, plane, category, accounted_at);
