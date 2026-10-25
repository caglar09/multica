-- Phase 3: Project Brain governance, context compilation and impact proposals.
-- Brain remains advisory knowledge: none of these tables may mutate the plan DAG.
ALTER TABLE autonomous_project_brain_entry
    ADD COLUMN IF NOT EXISTS authority TEXT NOT NULL DEFAULT 'agent_inference',
    ADD COLUMN IF NOT EXISTS evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS repository_revision TEXT,
    ADD COLUMN IF NOT EXISTS semantic_fingerprint TEXT,
    ADD COLUMN IF NOT EXISTS governance_state TEXT NOT NULL DEFAULT 'current',
    ADD COLUMN IF NOT EXISTS conflict_group_id UUID,
    ADD COLUMN IF NOT EXISTS brain_revision BIGINT NOT NULL DEFAULT 1;

UPDATE autonomous_project_brain_entry
SET observed_at = COALESCE(observed_at, created_at);

ALTER TABLE autonomous_project_brain_entry
    ALTER COLUMN observed_at SET NOT NULL;

ALTER TABLE autonomous_project_brain_entry
    DROP CONSTRAINT IF EXISTS autonomous_project_brain_entry_authority_check;
ALTER TABLE autonomous_project_brain_entry
    ADD CONSTRAINT autonomous_project_brain_entry_authority_check
    CHECK (authority IN (
        'user_decision',
        'authoritative_spec',
        'deterministic_observation',
        'trusted_external',
        'system_derived',
        'agent_inference'
    ));

ALTER TABLE autonomous_project_brain_entry
    DROP CONSTRAINT IF EXISTS autonomous_project_brain_entry_governance_state_check;
ALTER TABLE autonomous_project_brain_entry
    ADD CONSTRAINT autonomous_project_brain_entry_governance_state_check
    CHECK (governance_state IN ('current', 'stale', 'conflicted', 'superseded'));

ALTER TABLE autonomous_project_brain_entry
    DROP CONSTRAINT IF EXISTS autonomous_project_brain_entry_brain_revision_check;
ALTER TABLE autonomous_project_brain_entry
    ADD CONSTRAINT autonomous_project_brain_entry_brain_revision_check
    CHECK (brain_revision > 0);

CREATE TABLE IF NOT EXISTS autonomous_project_brain_state (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    revision BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (revision >= 0)
);

INSERT INTO autonomous_project_brain_state (project_id, workspace_id, revision)
SELECT project_id, MIN(workspace_id::text)::uuid, GREATEST(COALESCE(MAX(revision), 0), 1)
FROM autonomous_project_brain_entry
GROUP BY project_id
ON CONFLICT (project_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS autonomous_project_brain_snapshot (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID,
    plan_revision BIGINT,
    brain_revision BIGINT NOT NULL,
    entry_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (brain_revision >= 0),
    CHECK (plan_revision IS NULL OR plan_revision > 0)
);

CREATE TABLE IF NOT EXISTS autonomous_project_context_compilation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID,
    node_id UUID,
    issue_id UUID,
    workflow_action_id UUID UNIQUE,
    task_id UUID UNIQUE,
    role_family TEXT,
    total_token_budget INTEGER NOT NULL,
    used_tokens INTEGER NOT NULL,
    section_usage JSONB NOT NULL DEFAULT '{}'::jsonb,
    context_package JSONB NOT NULL,
    brain_snapshot_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (total_token_budget > 0),
    CHECK (used_tokens >= 0),
    CHECK (used_tokens <= total_token_budget)
);

CREATE TABLE IF NOT EXISTS autonomous_project_brain_impact_proposal (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID,
    brain_entry_id UUID NOT NULL UNIQUE,
    classification TEXT NOT NULL,
    affected_node_keys JSONB NOT NULL DEFAULT '[]'::jsonb,
    rationale TEXT NOT NULL,
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    proposed_plan_delta JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'proposed',
    analyzer TEXT NOT NULL DEFAULT 'deterministic_v1',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    CHECK (classification IN ('NO_IMPACT', 'MINOR_IMPACT', 'MAJOR_IMPACT')),
    CHECK (status IN ('proposed', 'reviewed', 'applied', 'rejected', 'superseded'))
);
