CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_deployment_operation_idempotency ON deployment_operation (workspace_id, idempotency_key);
