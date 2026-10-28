ALTER TABLE autonomous_project_quality_gate_run
    DROP CONSTRAINT IF EXISTS autonomous_project_quality_gate_run_status_check;

UPDATE autonomous_project_quality_gate_run
SET status = 'failed'
WHERE status IN ('blocked', 'unavailable', 'inconclusive', 'waived');

ALTER TABLE autonomous_project_quality_gate_run
    ADD CONSTRAINT autonomous_project_quality_gate_run_status_check
    CHECK (status IN ('pending', 'running', 'passed', 'failed', 'skipped'));
