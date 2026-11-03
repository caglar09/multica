CREATE OR REPLACE FUNCTION autonomous_project_specification_digest(target_revision_id UUID)
RETURNS TEXT LANGUAGE SQL STABLE AS $$
    SELECT encode(digest(convert_to(
        jsonb_build_object('goal', r.goal, 'specification', r.specification)::text,
        'UTF8'
    ), 'sha256'), 'hex')
    FROM autonomous_project_specification_revision r
    WHERE r.id = target_revision_id;
$$;

CREATE OR REPLACE FUNCTION autonomous_project_team_selection_digest(target_project_id UUID)
RETURNS TEXT LANGUAGE SQL STABLE AS $$
    SELECT encode(digest(convert_to(
        jsonb_build_object('plan', d.plan, 'selections', d.selections)::text,
        'UTF8'
    ), 'sha256'), 'hex')
    FROM autonomous_project_team_draft d
    WHERE d.project_id = target_project_id;
$$;

CREATE OR REPLACE FUNCTION autonomous_project_change_proposal_digest(target_change_request_id UUID)
RETURNS TEXT LANGUAGE SQL STABLE AS $$
    SELECT encode(digest(convert_to(
        jsonb_build_object(
            'request_type', r.request_type,
            'request_text', r.request_text,
            'proposal', r.proposal,
            'impact', r.impact,
            'base_specification_digest', autonomous_project_specification_digest(r.base_specification_revision_id),
            'proposed_specification_digest', autonomous_project_specification_digest(r.proposed_specification_revision_id),
            'base_plan_digest', autonomous_project_plan_digest(r.base_plan_id)
        )::text,
        'UTF8'
    ), 'sha256'), 'hex')
    FROM autonomous_project_change_request r
    WHERE r.id = target_change_request_id;
$$;
