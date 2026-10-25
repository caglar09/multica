CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_plan_identity
ON autonomous_project_plan (workspace_id, project_id, revision);