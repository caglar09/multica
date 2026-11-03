DROP FUNCTION IF EXISTS autonomous_project_plan_executable(UUID);
DROP FUNCTION IF EXISTS autonomous_project_specification_approved(UUID);

CREATE OR REPLACE FUNCTION autonomous_project_plan_executable(target_plan_id UUID)
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT COALESCE((
        SELECT p.status = 'active'
           AND NULLIF(p.plan_digest, '') IS NOT NULL
           AND p.approved_at IS NOT NULL
           AND p.plan_digest = autonomous_project_plan_digest(p.id)
        FROM autonomous_project_plan p
        WHERE p.id = target_plan_id
    ), FALSE);
$$;
