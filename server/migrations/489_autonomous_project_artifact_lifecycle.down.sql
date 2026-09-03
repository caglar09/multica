ALTER TABLE autonomous_project_artifact
    DROP CONSTRAINT IF EXISTS autonomous_project_artifact_revision_check,
    DROP CONSTRAINT IF EXISTS autonomous_project_artifact_status_check,
    DROP COLUMN IF EXISTS superseded_by,
    DROP COLUMN IF EXISTS artifact_revision,
    DROP COLUMN IF EXISTS validation_error,
    DROP COLUMN IF EXISTS valid,
    DROP COLUMN IF EXISTS status;
