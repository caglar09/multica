CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_escalation_open
ON autonomous_project_escalation (workspace_id, project_id, severity, opened_at DESC)
WHERE status IN ('open', 'acknowledged');