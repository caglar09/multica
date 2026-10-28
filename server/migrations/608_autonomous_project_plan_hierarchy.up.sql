ALTER TABLE autonomous_project_plan_node
    ADD COLUMN is_epic BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN parent_epic_key TEXT;

ALTER TABLE autonomous_project_plan_node
    ADD CONSTRAINT autonomous_project_plan_node_hierarchy_check
    CHECK (
        (is_epic = TRUE AND parent_epic_key IS NULL)
        OR (is_epic = FALSE AND (parent_epic_key IS NULL OR length(btrim(parent_epic_key)) > 0))
    );

CREATE OR REPLACE FUNCTION autonomous_project_plan_digest(target_plan_id UUID)
RETURNS TEXT LANGUAGE SQL STABLE AS $$
    WITH plan_data AS (
        SELECT p.specification_revision_id::text AS specification_revision_id,
               p.goal,
               p.policy,
               COALESCE((
                   SELECT jsonb_agg(
                       jsonb_build_object(
                           'node_key', n.node_key,
                           'kind', n.kind,
                           'title', n.title,
                           'description', n.description,
                           'priority', n.priority,
                           'required_role_family', n.required_role_family,
                           'required_capabilities', n.required_capabilities,
                           'acceptance_criteria', n.acceptance_criteria,
                           'requirement_ids', n.requirement_ids,
                           'risk_level', n.risk_level,
                           'max_attempts', n.max_attempts
                       ) || CASE
                           WHEN n.is_epic OR n.parent_epic_key IS NOT NULL THEN
                               jsonb_build_object('is_epic', n.is_epic, 'parent_epic_key', n.parent_epic_key)
                           ELSE '{}'::jsonb
                       END
                       ORDER BY n.node_key
                   )
                   FROM autonomous_project_plan_node n
                   WHERE n.plan_id = p.id
               ), '[]'::jsonb) AS nodes,
               COALESCE((
                   SELECT jsonb_agg(jsonb_build_object(
                       'from', e.from_node_key,
                       'to', e.to_node_key,
                       'dependency_type', e.dependency_type,
                       'required_artifact_type', e.required_artifact_type
                   ) ORDER BY e.from_node_key, e.to_node_key, e.dependency_type, e.required_artifact_type)
                   FROM autonomous_project_plan_edge e
                   WHERE e.plan_id = p.id
               ), '[]'::jsonb) AS edges
        FROM autonomous_project_plan p
        WHERE p.id = target_plan_id
    )
    SELECT encode(digest(convert_to(
        specification_revision_id || E'\n' ||
        jsonb_build_object('goal', goal, 'nodes', nodes, 'edges', edges)::text || E'\n' ||
        policy::text,
        'UTF8'
    ), 'sha256'), 'hex')
    FROM plan_data;
$$;

UPDATE autonomous_project_plan
SET plan_digest = autonomous_project_plan_digest(id);
