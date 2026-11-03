CREATE OR REPLACE FUNCTION autonomous_project_specification_approved(target_revision_id UUID)
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT COALESCE((
        SELECT h.specification_revision_id = r.id
           AND h.revision = r.revision
           AND COALESCE((
               SELECT e.decision = 'approved'
                  AND e.actor_type = 'human'
                  AND e.origin = 'local'
                  AND e.subject_digest = autonomous_project_specification_digest(r.id)
               FROM autonomous_project_approval_event e
               WHERE e.workspace_id = r.workspace_id
                 AND e.project_id = r.project_id
                 AND e.subject_type = 'specification_revision'
                 AND e.subject_id = r.id::text
                 AND e.subject_revision = r.revision
               ORDER BY e.acted_at DESC, e.id DESC
               LIMIT 1
           ), FALSE)
        FROM autonomous_project_specification_revision r
        JOIN autonomous_project_specification_head h
          ON h.workspace_id = r.workspace_id
         AND h.project_id = r.project_id
        WHERE r.id = target_revision_id
    ), FALSE);
$$;

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
