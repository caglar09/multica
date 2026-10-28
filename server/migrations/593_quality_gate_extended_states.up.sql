ALTER TABLE autonomous_project_quality_gate_run
    DROP CONSTRAINT IF EXISTS autonomous_project_quality_gate_run_status_check;

ALTER TABLE autonomous_project_quality_gate_run
    ADD CONSTRAINT autonomous_project_quality_gate_run_status_check
    CHECK (status IN (
        'pending',
        'running',
        'passed',
        'failed',
        'blocked',
        'unavailable',
        'inconclusive',
        'waived',
        'skipped'
    ));
