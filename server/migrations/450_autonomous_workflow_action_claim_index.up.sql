CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_workflow_action_claim
ON autonomous_workflow_action (status, available_at, created_at)
WHERE status IN ('pending', 'running');
