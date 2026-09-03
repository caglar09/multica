-- Phase 1: deterministic review verdicts and durable agent handoffs.
-- No foreign keys by repository policy; workspace/project/task lifecycle
-- cleanup remains application-owned like the existing autonomous tables.
CREATE TABLE autonomous_project_review_verdict (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    workflow_run_id UUID,
    review_task_id UUID NOT NULL UNIQUE,
    reviewer_agent_id UUID NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('approved', 'changes_requested')),
    summary TEXT NOT NULL DEFAULT '',
    artifact JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE autonomous_project_review_finding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    verdict_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    review_task_id UUID NOT NULL,
    finding_key TEXT NOT NULL,
    severity TEXT NOT NULL CHECK (severity IN ('low','medium','high','critical')),
    category TEXT NOT NULL CHECK (category ~ '^[a-z][a-z0-9_]{0,63}$'),
    description TEXT NOT NULL,
    evidence TEXT NOT NULL,
    blocking BOOLEAN NOT NULL DEFAULT TRUE,
    lifecycle_status TEXT NOT NULL DEFAULT 'open'
        CHECK (lifecycle_status IN ('open','resolved','waived','superseded')),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (verdict_id, finding_key)
);

CREATE TABLE autonomous_project_handoff (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    issue_id UUID,
    workflow_run_id UUID,
    workflow_action_id UUID UNIQUE,
    source_node_id UUID,
    source_node_key TEXT,
    source_task_id UUID,
    source_agent_id UUID,
    target_task_id UUID,
    target_agent_id UUID,
    handoff_kind TEXT NOT NULL CHECK (handoff_kind ~ '^[a-z][a-z0-9_]{0,63}$'),
    schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
    summary TEXT NOT NULL DEFAULT '',
    envelope JSONB NOT NULL,
    brain_context_tokens BIGINT NOT NULL DEFAULT 0 CHECK (brain_context_tokens >= 0),
    brain_context_estimated BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
