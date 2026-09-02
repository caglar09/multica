CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_plan_node_identity
ON autonomous_project_plan_node (plan_id, node_key);