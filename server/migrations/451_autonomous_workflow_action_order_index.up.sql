CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_workflow_action_order
ON autonomous_workflow_action (run_id, event_id, position);
