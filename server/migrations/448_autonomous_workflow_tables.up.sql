-- Durable state for deterministic autonomous issue workflows.
-- No foreign keys by repository policy: issue/agent/workspace lifecycle cleanup is
-- intentionally owned by application code. The identity and claim indexes live
-- in separate concurrent-index migrations (449-451).
CREATE TABLE IF NOT EXISTS autonomous_workflow_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_name TEXT NOT NULL,
    workflow_version INTEGER NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID,
    issue_id UUID NOT NULL,
    state TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    owner_agent_id UUID,
    reviewer_agent_id UUID,
    accountable_user_id UUID,
    review_cycles INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS autonomous_workflow_processed_event (
    event_id TEXT PRIMARY KEY,
    run_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS autonomous_workflow_action (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL,
    event_id TEXT NOT NULL,
    position SMALLINT NOT NULL,
    action_type TEXT NOT NULL,
    params JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (position >= 0),
    CHECK (attempts >= 0),
    CHECK (max_attempts > 0),
    CHECK (status IN ('pending', 'running', 'completed', 'failed'))
);
