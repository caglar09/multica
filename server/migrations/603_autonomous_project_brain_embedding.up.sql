CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE autonomous_project_brain_entry
    ADD COLUMN IF NOT EXISTS embedding vector(1536);
