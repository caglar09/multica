CREATE TABLE IF NOT EXISTS autonomous_control_plane_job (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    job_type TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'claimed', 'running', 'completed', 'failed', 'retry')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    priority INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, project_id, job_type, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_autonomous_control_plane_job_claim
ON autonomous_control_plane_job (status, available_at, priority DESC, created_at)
WHERE status IN ('pending', 'retry', 'claimed', 'running');

CREATE INDEX IF NOT EXISTS idx_autonomous_control_plane_job_lease
ON autonomous_control_plane_job (lease_expires_at)
WHERE status IN ('claimed', 'running');

CREATE INDEX IF NOT EXISTS idx_autonomous_control_plane_job_project
ON autonomous_control_plane_job (workspace_id, project_id, created_at DESC);
