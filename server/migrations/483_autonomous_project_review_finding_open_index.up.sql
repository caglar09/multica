CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_review_finding_open
ON autonomous_project_review_finding(workspace_id, project_id, issue_id, blocking, created_at)
WHERE lifecycle_status = 'open';
