CREATE TABLE IF NOT EXISTS deployment_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    operation_id UUID NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    provider_event_id TEXT,
    dedupe_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    observed_status TEXT
        CHECK (observed_status IS NULL OR observed_status IN ('queued', 'dispatching', 'running', 'retry_wait', 'succeeded', 'failed', 'canceled')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    applied BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
