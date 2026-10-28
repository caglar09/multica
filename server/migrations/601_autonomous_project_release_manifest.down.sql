DROP TRIGGER IF EXISTS autonomous_project_release_manifest_immutable
    ON autonomous_project_release_manifest;
DROP FUNCTION IF EXISTS reject_autonomous_project_release_manifest_mutation();
DROP TABLE IF EXISTS autonomous_project_release_manifest;

ALTER TABLE autonomous_project_plan
    DROP COLUMN IF EXISTS release_manifest_id,
    DROP COLUMN IF EXISTS release_manifest_digest;

ALTER TABLE autonomous_project_deployment
    DROP COLUMN IF EXISTS release_manifest_id,
    DROP COLUMN IF EXISTS release_manifest_digest;
