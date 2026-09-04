-- Phase 4: living/changeable project model.
-- Specification = authoritative user intent, Brain = learned project knowledge,
-- Plan = execution strategy derived from a specification revision.

CREATE TABLE autonomous_project_specification_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    source_kind TEXT NOT NULL CHECK (source_kind IN ('project_bootstrap','change_request','planner','backfill','system')),
    source_ref TEXT,
    goal TEXT NOT NULL,
    specification JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, project_id, revision)
);
CREATE INDEX idx_project_spec_revision_project
    ON autonomous_project_specification_revision(workspace_id, project_id, revision DESC);
CREATE UNIQUE INDEX uq_project_spec_revision_source
    ON autonomous_project_specification_revision(workspace_id, project_id, source_kind, source_ref)
    WHERE source_ref IS NOT NULL;

CREATE TABLE autonomous_project_specification_head (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    specification_revision_id UUID NOT NULL REFERENCES autonomous_project_specification_revision(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, project_id)
);

CREATE OR REPLACE FUNCTION autonomous_project_specification_immutable()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'autonomous project specification revisions are immutable';
END;
$$;
CREATE TRIGGER trg_autonomous_project_specification_immutable
BEFORE UPDATE ON autonomous_project_specification_revision
FOR EACH ROW EXECUTE FUNCTION autonomous_project_specification_immutable();

-- Convert every legacy plan specification into immutable historical intent.
INSERT INTO autonomous_project_specification_revision (
    workspace_id, project_id, revision, source_kind, source_ref, goal, specification, created_at
)
SELECT
    p.workspace_id, p.project_id, p.revision, 'backfill', 'plan:' || p.id::text, p.goal,
    jsonb_build_object(
        'goal', p.goal,
        'summary', COALESCE(p.specification->>'summary', p.goal),
        'requirements', COALESCE((
            SELECT jsonb_agg(jsonb_build_object(
                'id', 'legacy-' || r.ordinality::text,
                'text', r.value,
                'rationale', 'Preserved from autonomous plan revision ' || p.revision::text,
                'source', 'legacy_plan'
            ) ORDER BY r.ordinality)
            FROM jsonb_array_elements_text(COALESCE(p.specification->'requirements', '[]'::jsonb))
                 WITH ORDINALITY AS r(value, ordinality)
        ), '[]'::jsonb),
        'non_functional_requirements', COALESCE(p.specification->'non_functional_requirements', '[]'::jsonb),
        'constraints', COALESCE(p.specification->'constraints', '[]'::jsonb),
        'non_goals', '[]'::jsonb,
        'definition_of_done', COALESCE(p.specification->'definition_of_done', '[]'::jsonb),
        'acceptance_expectations', '[]'::jsonb
    ),
    p.created_at
FROM autonomous_project_plan p
ON CONFLICT (workspace_id, project_id, revision) DO NOTHING;

INSERT INTO autonomous_project_specification_head (workspace_id, project_id, specification_revision_id, revision)
SELECT DISTINCT ON (workspace_id, project_id) workspace_id, project_id, id, revision
FROM autonomous_project_specification_revision
ORDER BY workspace_id, project_id, revision DESC
ON CONFLICT (workspace_id, project_id) DO UPDATE
SET specification_revision_id = EXCLUDED.specification_revision_id,
    revision = EXCLUDED.revision,
    updated_at = now();

ALTER TABLE autonomous_project_plan
    ADD COLUMN specification_revision_id UUID REFERENCES autonomous_project_specification_revision(id);
UPDATE autonomous_project_plan p
SET specification_revision_id = s.id
FROM autonomous_project_specification_revision s
WHERE s.workspace_id = p.workspace_id AND s.project_id = p.project_id AND s.revision = p.revision;

-- New plans bind to current intent. For old projects without a head, first
-- persist bootstraps a normalized authoritative specification revision.
CREATE OR REPLACE FUNCTION autonomous_project_bind_plan_specification()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    spec_id UUID;
    next_revision BIGINT;
    normalized JSONB;
BEGIN
    IF NEW.specification_revision_id IS NOT NULL THEN RETURN NEW; END IF;
    SELECT specification_revision_id INTO spec_id
    FROM autonomous_project_specification_head
    WHERE workspace_id = NEW.workspace_id AND project_id = NEW.project_id;

    IF spec_id IS NULL THEN
        SELECT COALESCE(MAX(revision), 0) + 1 INTO next_revision
        FROM autonomous_project_specification_revision
        WHERE workspace_id = NEW.workspace_id AND project_id = NEW.project_id;

        normalized := jsonb_build_object(
            'goal', NEW.goal,
            'summary', COALESCE(NEW.specification->>'summary', NEW.goal),
            'requirements', COALESCE((
                SELECT jsonb_agg(jsonb_build_object(
                    'id', 'initial-' || r.ordinality::text,
                    'text', r.value,
                    'rationale', 'Initial project requirement supplied to the planner',
                    'source', 'initial_plan'
                ) ORDER BY r.ordinality)
                FROM jsonb_array_elements_text(COALESCE(NEW.specification->'requirements', '[]'::jsonb))
                     WITH ORDINALITY AS r(value, ordinality)
            ), '[]'::jsonb),
            'non_functional_requirements', COALESCE(NEW.specification->'non_functional_requirements', '[]'::jsonb),
            'constraints', COALESCE(NEW.specification->'constraints', '[]'::jsonb),
            'non_goals', '[]'::jsonb,
            'definition_of_done', COALESCE(NEW.specification->'definition_of_done', '[]'::jsonb),
            'acceptance_expectations', '[]'::jsonb
        );
        INSERT INTO autonomous_project_specification_revision (
            workspace_id, project_id, revision, source_kind, source_ref, goal, specification
        ) VALUES (NEW.workspace_id, NEW.project_id, next_revision, 'planner', NEW.source_revision, NEW.goal, normalized)
        RETURNING id INTO spec_id;
        INSERT INTO autonomous_project_specification_head (workspace_id, project_id, specification_revision_id, revision)
        VALUES (NEW.workspace_id, NEW.project_id, spec_id, next_revision)
        ON CONFLICT (workspace_id, project_id) DO UPDATE
        SET specification_revision_id = EXCLUDED.specification_revision_id,
            revision = EXCLUDED.revision,
            updated_at = now();
    END IF;
    NEW.specification_revision_id := spec_id;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_autonomous_project_bind_plan_specification
BEFORE INSERT ON autonomous_project_plan
FOR EACH ROW EXECUTE FUNCTION autonomous_project_bind_plan_specification();
ALTER TABLE autonomous_project_plan ALTER COLUMN specification_revision_id SET NOT NULL;
CREATE INDEX idx_autonomous_project_plan_spec_revision ON autonomous_project_plan(specification_revision_id);

-- Backend-owned logical identity. Stable node_key remains a compatibility fence,
-- but explicit UPDATE_NODE can carry this UUID across key/title renames.
ALTER TABLE autonomous_project_plan_node ADD COLUMN logical_node_id UUID;
WITH identity AS (
    SELECT DISTINCT ON (n.workspace_id, n.project_id, n.node_key)
           n.workspace_id, n.project_id, n.node_key, n.id AS logical_node_id
    FROM autonomous_project_plan_node n
    JOIN autonomous_project_plan p ON p.id = n.plan_id
    ORDER BY n.workspace_id, n.project_id, n.node_key, p.revision, n.created_at, n.id
)
UPDATE autonomous_project_plan_node n
SET logical_node_id = i.logical_node_id
FROM identity i
WHERE i.workspace_id = n.workspace_id AND i.project_id = n.project_id AND i.node_key = n.node_key;

CREATE OR REPLACE FUNCTION autonomous_project_assign_logical_node_id()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.logical_node_id IS NULL THEN
        SELECT prior.logical_node_id INTO NEW.logical_node_id
        FROM autonomous_project_plan_node prior
        JOIN autonomous_project_plan p ON p.id = prior.plan_id
        WHERE prior.workspace_id = NEW.workspace_id
          AND prior.project_id = NEW.project_id
          AND prior.node_key = NEW.node_key
          AND prior.logical_node_id IS NOT NULL
        ORDER BY p.revision DESC, prior.created_at DESC
        LIMIT 1;
    END IF;
    IF NEW.logical_node_id IS NULL THEN NEW.logical_node_id := gen_random_uuid(); END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_autonomous_project_assign_logical_node_id
BEFORE INSERT ON autonomous_project_plan_node
FOR EACH ROW EXECUTE FUNCTION autonomous_project_assign_logical_node_id();
ALTER TABLE autonomous_project_plan_node ALTER COLUMN logical_node_id SET NOT NULL;
CREATE UNIQUE INDEX uq_autonomous_project_plan_logical_node ON autonomous_project_plan_node(plan_id, logical_node_id);
CREATE INDEX idx_autonomous_project_logical_node ON autonomous_project_plan_node(workspace_id, project_id, logical_node_id);

CREATE TABLE autonomous_project_change_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    request_key TEXT NOT NULL,
    request_type TEXT NOT NULL CHECK (request_type IN ('feature','requirement_change','remove_feature','architecture_change','priority_change','team_change','policy_change','bug','question')),
    state TEXT NOT NULL DEFAULT 'received' CHECK (state IN ('received','analyzing','proposal_ready','approval_required','approved','applying','applied','rejected','failed')),
    source TEXT NOT NULL CHECK (source IN ('project_director','mika','system')),
    source_ref TEXT,
    request_text TEXT NOT NULL,
    proposal JSONB,
    impact JSONB,
    base_specification_revision_id UUID REFERENCES autonomous_project_specification_revision(id),
    proposed_specification_revision_id UUID REFERENCES autonomous_project_specification_revision(id),
    base_plan_id UUID REFERENCES autonomous_project_plan(id) ON DELETE SET NULL,
    applied_plan_id UUID REFERENCES autonomous_project_plan(id) ON DELETE SET NULL,
    approval_escalation_id UUID,
    error TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, project_id, request_key)
);
CREATE INDEX idx_autonomous_change_request_project_state
    ON autonomous_project_change_request(workspace_id, project_id, state, created_at DESC);

CREATE TABLE autonomous_project_change_request_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    change_request_id UUID NOT NULL REFERENCES autonomous_project_change_request(id) ON DELETE CASCADE,
    from_state TEXT,
    to_state TEXT NOT NULL,
    actor_type TEXT NOT NULL DEFAULT 'system',
    actor_ref TEXT,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_autonomous_change_request_event_request
    ON autonomous_project_change_request_event(change_request_id, created_at, id);
CREATE OR REPLACE FUNCTION autonomous_project_change_request_history()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO autonomous_project_change_request_event(change_request_id, from_state, to_state, details)
        VALUES (NEW.id, NULL, NEW.state, jsonb_build_object('source', NEW.source, 'request_type', NEW.request_type));
    ELSIF NEW.state IS DISTINCT FROM OLD.state THEN
        INSERT INTO autonomous_project_change_request_event(change_request_id, from_state, to_state, details)
        VALUES (NEW.id, OLD.state, NEW.state, '{}'::jsonb);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_autonomous_project_change_request_history
AFTER INSERT OR UPDATE OF state ON autonomous_project_change_request
FOR EACH ROW EXECUTE FUNCTION autonomous_project_change_request_history();

CREATE TABLE autonomous_project_plan_mutation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    change_request_id UUID NOT NULL REFERENCES autonomous_project_change_request(id) ON DELETE CASCADE,
    base_plan_id UUID REFERENCES autonomous_project_plan(id) ON DELETE SET NULL,
    applied_plan_id UUID REFERENCES autonomous_project_plan(id) ON DELETE SET NULL,
    operations JSONB NOT NULL,
    validation_state TEXT NOT NULL DEFAULT 'pending' CHECK (validation_state IN ('pending','validated','rejected','applied','failed')),
    validation_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_autonomous_plan_mutation_change_request ON autonomous_project_plan_mutation(change_request_id);

CREATE TABLE autonomous_project_node_retirement (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    change_request_id UUID NOT NULL REFERENCES autonomous_project_change_request(id) ON DELETE CASCADE,
    logical_node_id UUID NOT NULL,
    prior_plan_node_id UUID,
    materialized_issue_id UUID,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','claimed','retired','failed')),
    attempt INT NOT NULL DEFAULT 0,
    claimed_at TIMESTAMPTZ,
    retired_at TIMESTAMPTZ,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(change_request_id, logical_node_id)
);
CREATE INDEX idx_autonomous_node_retirement_pending
    ON autonomous_project_node_retirement(workspace_id, project_id, status, created_at);

-- Existing CleanupProject deletes autonomous plans. When the final plan goes
-- away, remove the Phase 4 project-scoped metadata without adding a second
-- application cleanup path. Workspace deletion remains handled by FK cascade.
CREATE OR REPLACE FUNCTION autonomous_project_cleanup_change_model_after_plan_delete()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM autonomous_project_plan
        WHERE workspace_id = OLD.workspace_id AND project_id = OLD.project_id
    ) THEN
        DELETE FROM autonomous_project_change_request WHERE workspace_id = OLD.workspace_id AND project_id = OLD.project_id;
        DELETE FROM autonomous_project_specification_head WHERE workspace_id = OLD.workspace_id AND project_id = OLD.project_id;
        DELETE FROM autonomous_project_specification_revision WHERE workspace_id = OLD.workspace_id AND project_id = OLD.project_id;
    END IF;
    RETURN OLD;
END;
$$;
CREATE TRIGGER trg_autonomous_project_cleanup_change_model_after_plan_delete
AFTER DELETE ON autonomous_project_plan
FOR EACH ROW EXECUTE FUNCTION autonomous_project_cleanup_change_model_after_plan_delete();
