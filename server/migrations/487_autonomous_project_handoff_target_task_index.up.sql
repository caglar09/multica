CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_handoff_target_task
ON autonomous_project_handoff(target_task_id)
WHERE target_task_id IS NOT NULL;
