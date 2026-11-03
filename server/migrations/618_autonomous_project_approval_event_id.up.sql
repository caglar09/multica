CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_autonomous_project_approval_event_id
    ON autonomous_project_approval_event (id);
