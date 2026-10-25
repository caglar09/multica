CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_deployment_event_dedupe ON deployment_event (provider, provider_event_id);
