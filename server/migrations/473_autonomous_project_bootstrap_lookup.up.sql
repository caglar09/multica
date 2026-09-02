CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_bootstrap_workspace ON autonomous_project_bootstrap(workspace_id, autonomy_mode, status);
