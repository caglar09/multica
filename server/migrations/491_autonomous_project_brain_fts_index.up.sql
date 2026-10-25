CREATE INDEX CONCURRENTLY IF NOT EXISTS autonomous_project_brain_entry_fts_idx
ON autonomous_project_brain_entry
USING GIN (to_tsvector('simple'::regconfig,
    COALESCE(subject, '') || ' ' || COALESCE(canonical_key, '') || ' ' || COALESCE(content::text, '')));
