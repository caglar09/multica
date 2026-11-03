CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_autonomous_project_approval_event_idempotency
    ON autonomous_project_approval_event (workspace_id, project_id, idempotency_key);
