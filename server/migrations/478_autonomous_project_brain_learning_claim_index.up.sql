CREATE INDEX CONCURRENTLY IF NOT EXISTS autonomous_project_brain_learning_claim_idx
ON autonomous_project_brain_learning_job (status, available_at, created_at)
WHERE status = 'pending';
