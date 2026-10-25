CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_plan_latest
ON autonomous_project_plan (workspace_id, project_id, revision DESC);