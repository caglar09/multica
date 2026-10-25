CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_handoff_issue
ON autonomous_project_handoff(workspace_id, project_id, issue_id, created_at DESC);
