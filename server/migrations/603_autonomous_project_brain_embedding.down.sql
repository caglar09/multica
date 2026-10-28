-- pgvector is shared database infrastructure, so rollback removes only this column.
ALTER TABLE autonomous_project_brain_entry
    DROP COLUMN IF EXISTS embedding;
