ALTER TABLE autonomous_project_plan
    ADD COLUMN plan_digest TEXT,
    ADD COLUMN approved_at TIMESTAMPTZ,
    ADD COLUMN approved_by TEXT;

DO $$
DECLARE
    status_constraint TEXT;
BEGIN
    SELECT conname INTO status_constraint
    FROM pg_constraint
    WHERE conrelid = 'autonomous_project_plan'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%status%';
    IF status_constraint IS NOT NULL THEN
        EXECUTE format('ALTER TABLE autonomous_project_plan DROP CONSTRAINT %I', status_constraint);
    END IF;
END $$;

ALTER TABLE autonomous_project_plan
    ADD CONSTRAINT autonomous_project_plan_status_check
    CHECK (status IN ('draft', 'awaiting_approval', 'active', 'superseded', 'completed', 'blocked', 'cancelled'));

CREATE OR REPLACE FUNCTION autonomous_project_plan_digest(target_plan_id UUID)
RETURNS TEXT LANGUAGE SQL STABLE AS $$
    WITH plan_data AS (
        SELECT p.specification_revision_id::text AS specification_revision_id,
               p.goal,
               p.policy,
               COALESCE((
                   SELECT jsonb_agg(jsonb_build_object(
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
                   ) ORDER BY n.node_key)
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

UPDATE autonomous_project_plan_edge e
SET required_artifact_type = CASE dep.kind
	WHEN 'product' THEN 'product_spec'
	WHEN 'architecture' THEN 'architecture'
	WHEN 'review' THEN 'review'
	WHEN 'qa' THEN 'qa_handoff'
	WHEN 'security' THEN 'security_review'
	WHEN 'integration' THEN 'integration_report'
	WHEN 'release' THEN 'release_manifest'
	WHEN 'deploy' THEN 'deployment_record'
	WHEN 'incident' THEN 'incident_report'
	ELSE 'implementation_handoff'
END
FROM autonomous_project_plan_node dep
WHERE e.dependency_type = 'artifact'
  AND dep.plan_id = e.plan_id
  AND dep.node_key = e.from_node_key;

UPDATE autonomous_project_plan
SET plan_digest = autonomous_project_plan_digest(id),
    status = CASE WHEN status IN ('draft', 'active', 'blocked') THEN 'awaiting_approval' ELSE status END,
    approved_at = NULL,
    approved_by = NULL;

ALTER TABLE autonomous_project_plan
    ALTER COLUMN plan_digest SET NOT NULL,
    ADD CONSTRAINT autonomous_project_plan_digest_format_check CHECK (plan_digest ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT autonomous_project_plan_approval_pair_check CHECK ((approved_at IS NULL) = (approved_by IS NULL));
