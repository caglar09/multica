CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS deployment_event_workspace_dedupe_uidx ON deployment_event (workspace_id, dedupe_key);
