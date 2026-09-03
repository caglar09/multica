CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_handoff_implementation_task
ON autonomous_project_handoff(source_task_id)
WHERE handoff_kind = 'implementation_result' AND source_task_id IS NOT NULL;
