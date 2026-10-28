ALTER TABLE autonomous_project_release_manifest
    DROP CONSTRAINT IF EXISTS autonomous_project_release_manifest_environment_check,
    ADD CONSTRAINT autonomous_project_release_manifest_environment_check
        CHECK (environment IN ('staging', 'production'));
