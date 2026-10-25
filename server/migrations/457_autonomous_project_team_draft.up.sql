-- Pending initial autonomous team configuration.
--
-- The LLM decides the proposed organization first, but specialist agents are
-- not created until a human chooses the runtime (and optional workspace skills)
-- for each role. This keeps provider selection explicit while preserving the
-- model-managed team composition.
CREATE TABLE IF NOT EXISTS autonomous_project_team_draft (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    plan JSONB NOT NULL,
    planner_name TEXT NOT NULL,
    planner_model TEXT,
    status TEXT NOT NULL DEFAULT 'awaiting_configuration',
    selections JSONB NOT NULL DEFAULT '{}'::jsonb,
    confirmed_at TIMESTAMPTZ,
    confirmed_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, project_id),
    CHECK (status IN ('awaiting_configuration', 'provisioning', 'applied', 'cancelled'))
);

CREATE INDEX IF NOT EXISTS idx_autonomous_project_team_draft_workspace_status
ON autonomous_project_team_draft (workspace_id, status, updated_at DESC);
