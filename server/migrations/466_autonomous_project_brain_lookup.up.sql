CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_autonomous_project_brain_lookup
ON autonomous_project_brain_entry (workspace_id, project_id, entry_type, created_at DESC)
WHERE superseded_by IS NULL;