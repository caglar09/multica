CREATE TABLE IF NOT EXISTS deployment_source_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    repository TEXT NOT NULL,
    git_ref TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    commit_message TEXT,
    daemon_id TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
