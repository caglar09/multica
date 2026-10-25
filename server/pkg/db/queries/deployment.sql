-- name: CreateDeploymentProviderConnection :one
INSERT INTO deployment_provider_connection (id, workspace_id, provider, name, secret_ref, webhook_secret_ref, config, enabled)
VALUES (COALESCE(sqlc.narg('id')::uuid, gen_random_uuid()), $1, $2, $3, sqlc.narg('secret_ref'), sqlc.narg('webhook_secret_ref'), $4, $5)
RETURNING *;

-- name: ListDeploymentProviderConnections :many
SELECT * FROM deployment_provider_connection WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: GetDeploymentProviderConnection :one
SELECT * FROM deployment_provider_connection WHERE workspace_id = $1 AND id = $2;

-- name: UpdateDeploymentProviderConnection :one
UPDATE deployment_provider_connection
SET name = $3, secret_ref = sqlc.narg('secret_ref'), webhook_secret_ref = sqlc.narg('webhook_secret_ref'), config = $4, enabled = $5, updated_at = now()
WHERE workspace_id = $1 AND id = $2
RETURNING *;

-- name: DeleteDeploymentProviderConnection :exec
DELETE FROM deployment_provider_connection WHERE workspace_id = $1 AND id = $2;

-- name: CreateDeploymentTarget :one
INSERT INTO deployment_target (id, workspace_id, connection_id, name, environment, external_id, config)
VALUES (COALESCE(sqlc.narg('id')::uuid, gen_random_uuid()), $1, $2, $3, $4, sqlc.narg('external_id'), $5)
RETURNING *;

-- name: ListDeploymentTargets :many
SELECT * FROM deployment_target WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: UpsertDeploymentSourceRevision :one
INSERT INTO deployment_source_revision (
    workspace_id, project_id, repository, git_ref, commit_sha,
    commit_message, daemon_id, observed_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
ON CONFLICT (workspace_id, project_id, repository) DO UPDATE SET
    git_ref = EXCLUDED.git_ref,
    commit_sha = EXCLUDED.commit_sha,
    commit_message = EXCLUDED.commit_message,
    daemon_id = EXCLUDED.daemon_id,
    observed_at = EXCLUDED.observed_at,
    updated_at = now()
RETURNING *;

-- name: GetDeploymentSourceRevision :one
SELECT * FROM deployment_source_revision
WHERE workspace_id = $1 AND project_id = $2 AND repository = $3;

-- name: GetDeploymentTarget :one
SELECT * FROM deployment_target WHERE workspace_id = $1 AND id = $2;

-- name: UpdateDeploymentTarget :one
UPDATE deployment_target
SET name = $3, environment = $4, external_id = sqlc.narg('external_id'), config = $5, updated_at = now()
WHERE workspace_id = $1 AND id = $2
RETURNING *;

-- name: DeleteDeploymentTarget :exec
DELETE FROM deployment_target WHERE workspace_id = $1 AND id = $2;

-- name: CreateDeploymentOperation :one
INSERT INTO deployment_operation (
    id, workspace_id, target_id, action, idempotency_key, source_revision,
    environment, operation_kind, approval_reference, status, max_attempts, request
) VALUES (
    COALESCE(sqlc.narg('id')::uuid, gen_random_uuid()), $1, $2, $3, $4, $5,
    $6, $7, $8, 'pending', $9, $10
)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetDeploymentOperation :one
SELECT * FROM deployment_operation WHERE workspace_id = $1 AND id = $2;

-- name: GetDeploymentOperationByIdempotency :one
SELECT * FROM deployment_operation
WHERE workspace_id = $1 AND target_id = $2 AND source_revision = $3
  AND environment = $4 AND operation_kind = $5 AND approval_reference = $6;

-- name: GetDeploymentOperationByKey :one
SELECT * FROM deployment_operation WHERE workspace_id = $1 AND idempotency_key = $2;

-- name: GetDeploymentOperationByProviderOperation :one
SELECT o.* FROM deployment_operation o
JOIN deployment_target t ON t.id = o.target_id AND t.workspace_id = o.workspace_id
WHERE o.workspace_id = $1 AND o.provider_operation_id = $2;

-- name: ListNonTerminalDeploymentOperations :many
SELECT * FROM deployment_operation
WHERE workspace_id = $1
  AND status NOT IN ('ready', 'succeeded', 'failed', 'cancelled', 'canceled');

-- name: ListDeploymentOperations :many
SELECT * FROM deployment_operation WHERE workspace_id = $1 AND target_id = $2 ORDER BY created_at DESC;

-- name: UpdateDeploymentOperation :one
UPDATE deployment_operation
SET status = $3, attempt_count = $4, max_attempts = $5, next_attempt_at = $6,
    lease_owner = $7, lease_expires_at = $8, provider_operation_id = $9,
    evidence = $10, last_error = $11, revision = revision + 1, updated_at = now()
WHERE workspace_id = $1 AND id = $2 AND revision = $12
RETURNING *;

-- name: CreateDeploymentEvent :one
INSERT INTO deployment_event (
    id, workspace_id, operation_id, provider, provider_event_id, dedupe_key,
    kind, observed_status, payload, applied
) VALUES (
    COALESCE(sqlc.narg('id')::uuid, gen_random_uuid()), $1, $2, $3,
    sqlc.narg('provider_event_id'), $4, $5, sqlc.narg('observed_status'), $6, $7
)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetDeploymentEventByProviderEvent :one
SELECT * FROM deployment_event WHERE provider = $1 AND provider_event_id = $2;

-- name: GetDeploymentEventByDedupeKey :one
SELECT * FROM deployment_event WHERE workspace_id = $1 AND dedupe_key = $2;

-- name: ListDeploymentEvents :many
SELECT * FROM deployment_event WHERE workspace_id = $1 AND operation_id = $2 ORDER BY created_at ASC;
