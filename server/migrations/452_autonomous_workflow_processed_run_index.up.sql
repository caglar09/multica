CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_workflow_processed_run
ON autonomous_workflow_processed_event (run_id);
