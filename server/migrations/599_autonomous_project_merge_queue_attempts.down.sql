ALTER TABLE autonomous_project_change_set
    DROP CONSTRAINT IF EXISTS autonomous_project_change_set_merge_attempt_check,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS merge_attempt;
