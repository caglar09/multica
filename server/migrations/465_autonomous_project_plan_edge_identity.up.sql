CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_plan_edge_identity
ON autonomous_project_plan_edge (plan_id, from_node_key, to_node_key, dependency_type);