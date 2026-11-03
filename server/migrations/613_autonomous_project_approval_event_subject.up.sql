CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_approval_event_subject
    ON autonomous_project_approval_event (workspace_id, project_id, subject_type, subject_id, acted_at);
