DROP TABLE IF EXISTS autonomous_project_brain_impact_proposal;
DROP TABLE IF EXISTS autonomous_project_context_compilation;
DROP TABLE IF EXISTS autonomous_project_brain_snapshot;
DROP TABLE IF EXISTS autonomous_project_brain_state;

ALTER TABLE autonomous_project_brain_entry
    DROP CONSTRAINT IF EXISTS autonomous_project_brain_entry_brain_revision_check,
    DROP CONSTRAINT IF EXISTS autonomous_project_brain_entry_governance_state_check,
    DROP CONSTRAINT IF EXISTS autonomous_project_brain_entry_authority_check,
    DROP COLUMN IF EXISTS brain_revision,
    DROP COLUMN IF EXISTS conflict_group_id,
    DROP COLUMN IF EXISTS governance_state,
    DROP COLUMN IF EXISTS semantic_fingerprint,
    DROP COLUMN IF EXISTS repository_revision,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS observed_at,
    DROP COLUMN IF EXISTS evidence,
    DROP COLUMN IF EXISTS authority;
