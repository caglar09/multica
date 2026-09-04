-- Phase 3 compatibility fence: legacy deterministic Brain writers predate the
-- governed retention path and do not always supply observed_at explicitly.
-- Keep NOT NULL semantics while making those inserts safe until every writer
-- is routed through governed retention.
ALTER TABLE autonomous_project_brain_entry
    ALTER COLUMN observed_at SET DEFAULT now();
