ALTER TABLE autonomous_project_quality_gate_run
    ADD COLUMN IF NOT EXISTS executed_command TEXT,
    ADD COLUMN IF NOT EXISTS exit_code INTEGER,
    ADD COLUMN IF NOT EXISTS worktree_path TEXT,
    ADD COLUMN IF NOT EXISTS commit_sha TEXT,
    ADD COLUMN IF NOT EXISTS runner_type TEXT;

ALTER TABLE autonomous_project_artifact
    DROP CONSTRAINT IF EXISTS autonomous_project_artifact_artifact_type_check;

ALTER TABLE autonomous_project_artifact
    ADD CONSTRAINT autonomous_project_artifact_artifact_type_check
    CHECK (artifact_type IN (
        'product_spec', 'architecture', 'api_contract', 'data_model', 'test_plan',
        'implementation_handoff', 'review', 'security_review', 'qa_report', 'qa_handoff',
        'integration_report', 'release_manifest', 'deployment_record', 'incident_report',
        'postmortem', 'generic'
    ));
