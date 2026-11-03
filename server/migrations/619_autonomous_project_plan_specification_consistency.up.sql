CREATE OR REPLACE FUNCTION autonomous_project_plan_matches_bound_specification(target_plan_id UUID)
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT COALESCE((
        SELECT p.goal IS NOT DISTINCT FROM r.goal
           AND (p.specification->'summary') IS NOT DISTINCT FROM (r.specification->'summary')
           AND COALESCE(NULLIF(p.specification->'requirement_details', 'null'::jsonb), '[]'::jsonb)
               = COALESCE(NULLIF(r.specification->'requirements', 'null'::jsonb), '[]'::jsonb)
           AND COALESCE(NULLIF(p.specification->'requirements', 'null'::jsonb), '[]'::jsonb)
               = COALESCE((
                   SELECT jsonb_agg(
                       -- Keep this trim set aligned with strings.TrimSpace in LegacySpecification.
                       btrim(req.value->>'text', U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000')
                       ORDER BY req.ordinality
                   )
                   FROM jsonb_array_elements(
                       CASE
                           WHEN jsonb_typeof(r.specification->'requirements') = 'array'
                               THEN r.specification->'requirements'
                           ELSE '[]'::jsonb
                       END
                   ) WITH ORDINALITY AS req(value, ordinality)
                   WHERE NULLIF(
                       btrim(req.value->>'text', U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'),
                       ''
                   ) IS NOT NULL
               ), '[]'::jsonb)
           AND COALESCE(NULLIF(p.specification->'non_functional_requirements', 'null'::jsonb), '[]'::jsonb)
               = COALESCE(NULLIF(r.specification->'non_functional_requirements', 'null'::jsonb), '[]'::jsonb)
           AND COALESCE(NULLIF(p.specification->'constraints', 'null'::jsonb), '[]'::jsonb)
               = COALESCE(NULLIF(r.specification->'constraints', 'null'::jsonb), '[]'::jsonb)
           AND COALESCE(NULLIF(p.specification->'non_goals', 'null'::jsonb), '[]'::jsonb)
               = COALESCE(NULLIF(r.specification->'non_goals', 'null'::jsonb), '[]'::jsonb)
           AND COALESCE(NULLIF(p.specification->'definition_of_done', 'null'::jsonb), '[]'::jsonb)
               = COALESCE(NULLIF(r.specification->'definition_of_done', 'null'::jsonb), '[]'::jsonb)
           AND COALESCE(NULLIF(p.specification->'acceptance_expectations', 'null'::jsonb), '[]'::jsonb)
               = COALESCE(NULLIF(r.specification->'acceptance_expectations', 'null'::jsonb), '[]'::jsonb)
        FROM autonomous_project_plan p
        JOIN autonomous_project_specification_revision r
          ON r.id = p.specification_revision_id
         AND r.workspace_id = p.workspace_id
         AND r.project_id = p.project_id
        WHERE p.id = target_plan_id
          AND p.specification_revision_id IS NOT NULL
    ), FALSE);
$$;

CREATE OR REPLACE FUNCTION autonomous_project_plan_executable(target_plan_id UUID)
RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
    SELECT COALESCE((
        SELECT p.status = 'active'
           AND NULLIF(p.plan_digest, '') IS NOT NULL
           AND p.approved_at IS NOT NULL
           AND p.plan_digest = autonomous_project_plan_digest(p.id)
           AND autonomous_project_plan_matches_bound_specification(p.id)
           AND autonomous_project_specification_approved(p.specification_revision_id)
        FROM autonomous_project_plan p
        WHERE p.id = target_plan_id
    ), FALSE);
$$;
