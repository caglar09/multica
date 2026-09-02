ALTER TABLE autonomous_project_team_draft
    DROP CONSTRAINT IF EXISTS autonomous_project_team_draft_status_check;

ALTER TABLE autonomous_project_team_draft
    ADD CONSTRAINT autonomous_project_team_draft_status_check
    CHECK (status IN ('awaiting_configuration', 'provisioning', 'applied', 'cancelled'));
