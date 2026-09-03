CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_review_finding_open
    ON autonomous_project_review_finding(workspace_id, project_id, issue_id, blocking, created_at)
    WHERE lifecycle_status = 'open';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_review_verdict_issue
    ON autonomous_project_review_verdict(workspace_id, project_id, issue_id, created_at DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_handoff_issue
    ON autonomous_project_handoff(workspace_id, project_id, issue_id, created_at DESC);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_handoff_implementation_task
    ON autonomous_project_handoff(source_task_id)
    WHERE handoff_kind = 'implementation_result' AND source_task_id IS NOT NULL;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_handoff_target_task
    ON autonomous_project_handoff(target_task_id)
    WHERE target_task_id IS NOT NULL;
