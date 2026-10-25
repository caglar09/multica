-- The directed issue dependency graph. The application owns workspace and
-- cycle checks; this table stores the blocked_by edge.

-- name: IssueHasUnresolvedDependencies :one
SELECT EXISTS (
    SELECT 1
    FROM issue_dependency d
    JOIN issue predecessor
      ON predecessor.id = d.depends_on_issue_id
     AND predecessor.workspace_id = sqlc.arg('workspace_id')::uuid
    WHERE d.issue_id = sqlc.arg('issue_id')::uuid
      AND d.type = 'blocked_by'
      AND issue_effective_status(predecessor.workspace_id, predecessor.status)
          NOT IN ('done', 'cancelled')
) AS blocked;

-- name: ListIssueDependencies :many
SELECT d.id,
       d.issue_id,
       d.depends_on_issue_id,
       d.type,
       related.id AS related_id,
       related.workspace_id AS related_workspace_id,
       related.number AS related_number,
       related.title AS related_title,
       related.status AS related_status,
       owner.id AS owner_id,
       owner.number AS owner_number,
       owner.title AS owner_title,
       owner.status AS owner_status
FROM issue_dependency d
JOIN issue owner
  ON owner.id = d.issue_id
 AND owner.workspace_id = sqlc.arg('workspace_id')::uuid
JOIN issue related
  ON related.id = d.depends_on_issue_id
 AND related.workspace_id = owner.workspace_id
WHERE d.type = 'blocked_by'
  AND (d.issue_id = ANY(sqlc.arg('issue_ids')::uuid[])
       OR d.depends_on_issue_id = ANY(sqlc.arg('issue_ids')::uuid[]))
ORDER BY d.id;

-- name: IssueDependencyWouldCycle :one
WITH RECURSIVE reachable(issue_id) AS (
    SELECT d.depends_on_issue_id
    FROM issue_dependency d
    JOIN issue owner ON owner.id = d.issue_id
    WHERE d.issue_id = sqlc.arg('candidate_predecessor_id')::uuid
      AND owner.workspace_id = sqlc.arg('workspace_id')::uuid
      AND d.type = 'blocked_by'
    UNION
    SELECT d.depends_on_issue_id
    FROM issue_dependency d
    JOIN reachable r ON r.issue_id = d.issue_id
    WHERE d.type = 'blocked_by'
)
SELECT EXISTS (
    SELECT 1 FROM reachable
    WHERE issue_id = sqlc.arg('issue_id')::uuid
) AS would_cycle;

-- name: CreateIssueDependency :one
INSERT INTO issue_dependency (issue_id, depends_on_issue_id, type)
VALUES (
    sqlc.arg('issue_id')::uuid,
    sqlc.arg('depends_on_issue_id')::uuid,
    'blocked_by'
)
ON CONFLICT (issue_id, depends_on_issue_id, type) DO NOTHING
RETURNING *;

-- name: DeleteIssueDependency :one
DELETE FROM issue_dependency
WHERE id = sqlc.arg('id')::uuid
  AND issue_id = sqlc.arg('issue_id')::uuid
  AND type = 'blocked_by'
RETURNING *;
