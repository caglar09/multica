DROP INDEX IF EXISTS idx_autonomous_project_team_draft_continuation;

ALTER TABLE autonomous_project_team_draft
    DROP COLUMN IF EXISTS continuation_completed_at,
    DROP COLUMN IF EXISTS continuation_started_at,
    DROP COLUMN IF EXISTS continuation_task_id,
    DROP COLUMN IF EXISTS continuation_session_id;
