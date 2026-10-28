CREATE TABLE IF NOT EXISTS autonomous_project_release_manifest (
    manifest_id UUID NOT NULL,
    manifest_digest TEXT NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID NOT NULL,
    change_set_id UUID NOT NULL,
    plan_revision BIGINT NOT NULL,
    plan_digest TEXT NOT NULL,
    environment TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    tree_sha TEXT NOT NULL,
    builder TEXT NOT NULL,
    outputs TEXT[] NOT NULL,
    artifact_digest TEXT NOT NULL,
    artifact_storage_key TEXT NOT NULL,
    quality_gate_run_ids UUID[] NOT NULL,
    manifest_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (length(manifest_digest) = 64),
    CHECK (length(plan_digest) = 64),
    CHECK (length(artifact_digest) = 64),
    CHECK (environment = 'production'),
    CHECK (builder IN ('go_binary', 'pnpm_build')),
    CHECK (cardinality(outputs) > 0),
    CHECK (cardinality(quality_gate_run_ids) >= 2)
);

ALTER TABLE autonomous_project_plan
    ADD COLUMN IF NOT EXISTS release_manifest_id UUID,
    ADD COLUMN IF NOT EXISTS release_manifest_digest TEXT;

ALTER TABLE autonomous_project_deployment
    ADD COLUMN IF NOT EXISTS release_manifest_id UUID,
    ADD COLUMN IF NOT EXISTS release_manifest_digest TEXT;

CREATE OR REPLACE FUNCTION reject_autonomous_project_release_manifest_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'autonomous project release manifests are immutable';
END;
$$;

DROP TRIGGER IF EXISTS autonomous_project_release_manifest_immutable
    ON autonomous_project_release_manifest;
CREATE TRIGGER autonomous_project_release_manifest_immutable
    BEFORE UPDATE OR DELETE ON autonomous_project_release_manifest
    FOR EACH ROW EXECUTE FUNCTION reject_autonomous_project_release_manifest_mutation();
