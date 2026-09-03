-- Phase 2 artifact lifecycle metadata. Content stays immutable; lifecycle
-- metadata is mutable and is what dependency readiness evaluates.
ALTER TABLE autonomous_project_artifact
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active',
    ADD COLUMN valid BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN validation_error TEXT,
    ADD COLUMN artifact_revision BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN superseded_by UUID;

ALTER TABLE autonomous_project_artifact
    ADD CONSTRAINT autonomous_project_artifact_status_check
    CHECK (status IN ('active','invalid','superseded','waived'));
ALTER TABLE autonomous_project_artifact
    ADD CONSTRAINT autonomous_project_artifact_revision_check
    CHECK (artifact_revision > 0);

UPDATE autonomous_project_artifact a
SET status = 'invalid',
    valid = FALSE,
    validation_error = 'legacy artifact predates Phase 2 contract validation',
    artifact_revision = COALESCE((
        SELECT n.spec_revision
        FROM autonomous_project_plan_node n
        WHERE n.id = a.node_id
    ), 1)
WHERE COALESCE(a.content #>> '{contract,status}', '') = 'invalid'
   OR COALESCE((a.content #>> '{contract,valid}')::boolean, TRUE) = FALSE;
