#!/usr/bin/env bash
set -euo pipefail

PROJECT_ID="${1:-}"
if [[ -z "$PROJECT_ID" ]]; then
  echo "usage: $0 <project-id>" >&2
  exit 2
fi

COMPOSE_ARGS=(
  -f docker-compose.selfhost.yml
  -f docker-compose.selfhost.build.yml
)

psql_project() {
  docker compose "${COMPOSE_ARGS[@]}" exec -T postgres sh -lc \
    'psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v project_id="$1"' \
    -- "$PROJECT_ID"
}

section() {
  printf '\n\n===== %s =====\n' "$1"
}

section "PROJECT CONTROL"
psql_project <<'SQL'
SELECT project_id,
       workspace_id,
       paused,
       last_error,
       updated_at
FROM autonomous_project_control
WHERE project_id = :'project_id'::uuid;
SQL

section "BOOTSTRAP + BUDGET"
psql_project <<'SQL'
SELECT b.project_id,
       b.autonomy_mode,
       b.autonomy_level,
       b.status AS bootstrap_status,
       budget.total_attempts,
       budget.max_total_attempts,
       budget.tokens_used,
       budget.token_limit,
       budget.runtime_seconds_used,
       budget.runtime_seconds_limit,
       budget.cost_microunits_used,
       budget.cost_microunits_limit
FROM autonomous_project_bootstrap b
LEFT JOIN autonomous_project_budget budget
  ON budget.workspace_id = b.workspace_id
 AND budget.project_id = b.project_id
WHERE b.project_id = :'project_id'::uuid;
SQL

section "ACTIVE PLAN NODES"
psql_project <<'SQL'
SELECT p.revision AS plan_revision,
       n.node_key,
       n.kind,
       n.title,
       n.status AS node_status,
       n.attempt,
       n.max_attempts,
       n.required_role_family,
       n.required_capabilities,
       n.assigned_role,
       n.assigned_agent_id,
       n.blocked_category,
       n.blocked_reason,
       n.materialized_issue_id,
       i.status AS issue_status,
       n.updated_at
FROM autonomous_project_plan p
JOIN autonomous_project_plan_node n ON n.plan_id = p.id
LEFT JOIN issue i ON i.id = n.materialized_issue_id
WHERE p.project_id = :'project_id'::uuid
  AND p.status IN ('active','blocked')
ORDER BY p.revision DESC, n.position ASC, n.created_at ASC;
SQL

section "TEAM + RUNTIME ELIGIBILITY"
psql_project <<'SQL'
WITH active_team AS (
  SELECT t.id,
         t.workspace_id,
         t.project_id,
         t.plan
  FROM autonomous_project_team t
  WHERE t.project_id = :'project_id'::uuid
    AND t.status = 'active'
), role_plan AS (
  SELECT t.id AS team_id,
         t.workspace_id,
         t.project_id,
         role.value ->> 'role' AS role,
         role.value ->> 'family' AS family,
         COALESCE(role.value -> 'capabilities', '[]'::jsonb) AS capabilities
  FROM active_team t
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(t.plan -> 'roles', '[]'::jsonb)) role(value)
)
SELECT rp.role,
       rp.family,
       rp.capabilities,
       m.agent_id,
       a.name AS agent_name,
       a.status AS agent_status,
       a.runtime_id,
       ar.status AS runtime_status,
       ar.last_seen_at,
       (a.archived_at IS NULL) AS not_archived,
       (a.status = 'active') AS agent_active,
       (ar.status = 'online') AS runtime_online,
       (ar.last_seen_at > now() - interval '2 minutes') AS runtime_fresh,
       (
         a.archived_at IS NULL
         AND a.status = 'active'
         AND ar.status = 'online'
         AND ar.last_seen_at > now() - interval '2 minutes'
       ) AS scheduler_runtime_eligible
FROM role_plan rp
LEFT JOIN autonomous_project_team_member m
  ON m.team_id = rp.team_id
 AND m.role = rp.role
LEFT JOIN agent a ON a.id = m.agent_id
LEFT JOIN agent_runtime ar ON ar.id = a.runtime_id
ORDER BY rp.family, rp.role;
SQL

section "NODE x TEAM FAMILY MATRIX"
psql_project <<'SQL'
WITH active_plan AS (
  SELECT p.id
  FROM autonomous_project_plan p
  WHERE p.project_id = :'project_id'::uuid
    AND p.status IN ('active','blocked')
  ORDER BY p.revision DESC
  LIMIT 1
), active_team AS (
  SELECT t.id, t.plan
  FROM autonomous_project_team t
  WHERE t.project_id = :'project_id'::uuid
    AND t.status = 'active'
  LIMIT 1
), roles AS (
  SELECT t.id AS team_id,
         role.value ->> 'role' AS role,
         role.value ->> 'family' AS family,
         COALESCE(role.value -> 'capabilities', '[]'::jsonb) AS capabilities
  FROM active_team t
  CROSS JOIN LATERAL jsonb_array_elements(COALESCE(t.plan -> 'roles', '[]'::jsonb)) role(value)
)
SELECT n.node_key,
       n.kind,
       n.status AS node_status,
       n.required_role_family,
       n.required_capabilities,
       r.role AS candidate_role,
       r.family AS candidate_family,
       r.capabilities AS candidate_capabilities,
       m.agent_id,
       a.status AS agent_status,
       ar.status AS runtime_status,
       ar.last_seen_at,
       (r.family = n.required_role_family) AS family_match,
       (
         a.archived_at IS NULL
         AND a.status = 'active'
         AND ar.status = 'online'
         AND ar.last_seen_at > now() - interval '2 minutes'
       ) AS runtime_eligible
FROM autonomous_project_plan_node n
JOIN active_plan p ON p.id = n.plan_id
CROSS JOIN roles r
LEFT JOIN autonomous_project_team_member m
  ON m.team_id = r.team_id
 AND m.role = r.role
LEFT JOIN agent a ON a.id = m.agent_id
LEFT JOIN agent_runtime ar ON ar.id = a.runtime_id
WHERE n.status IN ('ready','blocked','pending')
ORDER BY n.position ASC, family_match DESC, runtime_eligible DESC, r.family, r.role;
SQL

section "OPEN ESCALATIONS"
psql_project <<'SQL'
SELECT e.category,
       e.severity,
       e.summary,
       e.status,
       e.context,
       e.created_at,
       e.updated_at
FROM autonomous_project_escalation e
WHERE e.project_id = :'project_id'::uuid
  AND e.status IN ('open','acknowledged')
ORDER BY e.created_at DESC;
SQL

section "ISSUE WORKFLOWS"
psql_project <<'SQL'
SELECT i.id AS issue_id,
       i.title,
       i.status AS issue_status,
       r.workflow_key,
       r.state AS workflow_state,
       r.attempt,
       r.updated_at
FROM issue i
LEFT JOIN autonomous_workflow_run r
  ON r.workspace_id = i.workspace_id
 AND r.issue_id = i.id
WHERE i.project_id = :'project_id'::uuid
ORDER BY i.created_at ASC, r.updated_at DESC;
SQL

section "WORKFLOW ACTIONS"
psql_project <<'SQL'
SELECT a.id,
       a.run_id,
       a.action_type,
       a.status,
       a.attempt,
       a.max_attempts,
       a.last_error,
       a.available_at,
       a.updated_at
FROM autonomous_workflow_action a
JOIN autonomous_workflow_run r ON r.id = a.run_id
JOIN issue i ON i.id = r.issue_id
WHERE i.project_id = :'project_id'::uuid
ORDER BY a.updated_at DESC
LIMIT 200;
SQL

section "WORKFLOW EVENTS"
psql_project <<'SQL'
SELECT e.id,
       e.run_id,
       e.event_type,
       e.created_at,
       e.payload
FROM autonomous_workflow_event e
JOIN autonomous_workflow_run r ON r.id = e.run_id
JOIN issue i ON i.id = r.issue_id
WHERE i.project_id = :'project_id'::uuid
ORDER BY e.created_at DESC
LIMIT 200;
SQL
