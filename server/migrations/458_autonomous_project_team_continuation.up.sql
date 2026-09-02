ALTER TABLE autonomous_project_team_draft
    ADD COLUMN IF NOT EXISTS continuation_session_id UUID,
    ADD COLUMN IF NOT EXISTS continuation_task_id UUID,
    ADD COLUMN IF NOT EXISTS continuation_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS continuation_completed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_autonomous_project_team_draft_continuation
ON autonomous_project_team_draft (workspace_id, updated_at)
WHERE status = 'applied' AND continuation_completed_at IS NULL;
