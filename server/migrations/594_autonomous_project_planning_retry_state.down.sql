UPDATE autonomous_project_team_draft
SET status = 'cancelled'
WHERE status = 'planning_failed';

ALTER TABLE autonomous_project_team_draft
    DROP CONSTRAINT IF EXISTS autonomous_project_team_draft_status_check;

ALTER TABLE autonomous_project_team_draft
    ADD CONSTRAINT autonomous_project_team_draft_status_check
    CHECK (status IN ('planning', 'awaiting_configuration', 'provisioning', 'applied', 'cancelled'));

ALTER TABLE autonomous_project_team_draft
    DROP COLUMN IF EXISTS next_planning_retry_at,
    DROP COLUMN IF EXISTS planning_attempts,
    DROP COLUMN IF EXISTS planning_lease_until,
    DROP COLUMN IF EXISTS planning_lease_owner;
