CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_workflow_run_identity
ON autonomous_workflow_run (workflow_name, workspace_id, issue_id);
