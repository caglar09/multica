CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS deployment_operation_workspace_idempotency_uidx ON deployment_operation (workspace_id, idempotency_key);
