ALTER TABLE autonomous_project_team_draft
    ADD COLUMN IF NOT EXISTS planning_lease_owner TEXT,
    ADD COLUMN IF NOT EXISTS planning_lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS planning_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_planning_retry_at TIMESTAMPTZ;

ALTER TABLE autonomous_project_team_draft
    DROP CONSTRAINT IF EXISTS autonomous_project_team_draft_status_check;

ALTER TABLE autonomous_project_team_draft
    ADD CONSTRAINT autonomous_project_team_draft_status_check
    CHECK (status IN ('planning', 'awaiting_configuration', 'provisioning', 'applied', 'cancelled', 'planning_failed'));
