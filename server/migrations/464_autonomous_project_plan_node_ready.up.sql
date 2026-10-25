CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_plan_node_ready
ON autonomous_project_plan_node (workspace_id, project_id, status, priority DESC, created_at)
WHERE status IN ('pending', 'ready', 'running', 'verification');