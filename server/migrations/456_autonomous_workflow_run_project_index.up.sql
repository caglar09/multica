CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_workflow_run_project
ON autonomous_workflow_run (workspace_id, project_id, updated_at DESC);
