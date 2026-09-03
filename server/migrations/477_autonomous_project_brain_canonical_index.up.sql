CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS autonomous_project_brain_canonical_active_idx
ON autonomous_project_brain_entry (workspace_id, project_id, canonical_key)
WHERE canonical_key IS NOT NULL AND status = 'active' AND superseded_by IS NULL;
