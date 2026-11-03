DROP FUNCTION IF EXISTS autonomous_project_plan_executable(UUID);
DROP FUNCTION IF EXISTS autonomous_project_plan_matches_bound_specification(UUID);

CREATE OR REPLACE FUNCTION autonomous_project_plan_executable(target_plan_id UUID)
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT COALESCE((
        SELECT p.status = 'active'
           AND NULLIF(p.plan_digest, '') IS NOT NULL
           AND p.approved_at IS NOT NULL
           AND p.plan_digest = autonomous_project_plan_digest(p.id)
           AND p.specification_revision_id IS NOT NULL
           AND p.specification->'requirement_details' = r.specification->'requirements'
           AND r.workspace_id = p.workspace_id
           AND r.project_id = p.project_id
           AND autonomous_project_specification_approved(r.id)
        FROM autonomous_project_plan p
        JOIN autonomous_project_specification_revision r
          ON r.id = p.specification_revision_id
        WHERE p.id = target_plan_id
    ), FALSE);
$$;
