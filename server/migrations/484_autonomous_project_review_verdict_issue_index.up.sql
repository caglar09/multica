CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_review_verdict_issue
ON autonomous_project_review_verdict(workspace_id, project_id, issue_id, created_at DESC);
