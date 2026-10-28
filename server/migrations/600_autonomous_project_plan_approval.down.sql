ALTER TABLE autonomous_project_plan
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_approval_pair_check,
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_digest_format_check;

UPDATE autonomous_project_plan
SET status = 'blocked'
WHERE status = 'awaiting_approval';

ALTER TABLE autonomous_project_plan
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_status_check,
    ADD CONSTRAINT autonomous_project_plan_status_check
    CHECK (status IN ('draft', 'active', 'superseded', 'completed', 'blocked', 'cancelled'));

DROP FUNCTION IF EXISTS autonomous_project_plan_digest(UUID);

ALTER TABLE autonomous_project_plan
    DROP COLUMN IF EXISTS plan_digest,
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS approved_by;
