CREATE TABLE autonomous_project_bootstrap (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    autonomy_mode TEXT NOT NULL DEFAULT 'standard'
        CHECK (autonomy_mode IN ('standard', 'autonomous')),
    autonomy_level TEXT NOT NULL DEFAULT 'development'
        CHECK (autonomy_level IN ('assisted', 'development', 'delivery', 'closed_loop')),
    brief TEXT NOT NULL DEFAULT '',
    knowledge JSONB NOT NULL DEFAULT '[]'::jsonb,
    policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    budget JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'ready'
        CHECK (status IN ('draft', 'ready', 'started', 'cancelled')),
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
