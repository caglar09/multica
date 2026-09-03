CREATE TABLE IF NOT EXISTS autonomous_project_brain_config (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    runtime_mode TEXT NOT NULL DEFAULT 'inherit_mika',
    runtime_id UUID,
    model TEXT,
    thinking_level TEXT,
    service_tier TEXT,
    learning_mode TEXT NOT NULL DEFAULT 'adaptive',
    updated_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (runtime_mode IN ('inherit_mika', 'custom')),
    CHECK (learning_mode IN ('deterministic', 'assisted', 'adaptive')),
    CHECK ((runtime_mode = 'inherit_mika' AND runtime_id IS NULL) OR
           (runtime_mode = 'custom' AND runtime_id IS NOT NULL))
);

ALTER TABLE autonomous_project_brain_entry
    ADD COLUMN IF NOT EXISTS canonical_key TEXT,
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS importance DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    ADD COLUMN IF NOT EXISTS confirmation_count INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS useful_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS harmful_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_confirmed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS autonomous_project_brain_learning_job (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    task_id UUID NOT NULL,
    evidence JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT,
    provider TEXT,
    model TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CHECK (status IN ('pending', 'running', 'completed', 'deferred')),
    CHECK (attempts >= 0),
    CHECK (max_attempts > 0)
);
