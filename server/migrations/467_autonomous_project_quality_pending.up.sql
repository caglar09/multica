CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_quality_pending
ON autonomous_project_quality_gate_run (workspace_id, project_id, status, created_at)
WHERE status IN ('pending', 'running', 'failed');