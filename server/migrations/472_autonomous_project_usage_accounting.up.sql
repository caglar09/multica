CREATE TABLE autonomous_project_usage_accounting (
    task_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    tokens BIGINT NOT NULL DEFAULT 0 CHECK (tokens >= 0),
    runtime_seconds BIGINT NOT NULL DEFAULT 0 CHECK (runtime_seconds >= 0),
    cost_microunits BIGINT NOT NULL DEFAULT 0 CHECK (cost_microunits >= 0),
    accounted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_autonomous_project_usage_accounting_project
    ON autonomous_project_usage_accounting(workspace_id, project_id, accounted_at);
