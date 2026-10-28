DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM autonomous_project_release_manifest
        WHERE environment = 'staging'
    ) THEN
        RAISE EXCEPTION 'cannot restore production-only manifest environment constraint while staging manifests exist';
    END IF;
END;
$$;

ALTER TABLE autonomous_project_release_manifest
    DROP CONSTRAINT IF EXISTS autonomous_project_release_manifest_environment_check,
    ADD CONSTRAINT autonomous_project_release_manifest_environment_check
        CHECK (environment = 'production');
