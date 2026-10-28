UPDATE autonomous_project_artifact
SET artifact_type = 'qa_report'
WHERE artifact_type = 'qa_handoff';

UPDATE autonomous_project_plan_edge
SET required_artifact_type = 'qa_report'
WHERE required_artifact_type = 'qa_handoff';

ALTER TABLE autonomous_project_artifact
    DROP CONSTRAINT IF EXISTS autonomous_project_artifact_artifact_type_check;

ALTER TABLE autonomous_project_artifact
    ADD CONSTRAINT autonomous_project_artifact_artifact_type_check
    CHECK (artifact_type IN (
        'product_spec', 'architecture', 'api_contract', 'data_model', 'test_plan',
        'implementation_handoff', 'review', 'security_review', 'qa_report',
        'integration_report', 'release_manifest', 'deployment_record', 'incident_report',
        'postmortem', 'generic'
    ));

ALTER TABLE autonomous_project_quality_gate_run
    DROP COLUMN IF EXISTS runner_type,
    DROP COLUMN IF EXISTS commit_sha,
    DROP COLUMN IF EXISTS worktree_path,
    DROP COLUMN IF EXISTS exit_code,
    DROP COLUMN IF EXISTS executed_command;
