CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_deployment_source_revision_identity
    ON deployment_source_revision (workspace_id, project_id, repository);
