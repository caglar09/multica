DROP TABLE IF EXISTS autonomous_project_brain_learning_job;
DROP TABLE IF EXISTS autonomous_project_brain_config;

ALTER TABLE autonomous_project_brain_entry
    DROP COLUMN IF EXISTS last_confirmed_at,
    DROP COLUMN IF EXISTS harmful_count,
    DROP COLUMN IF EXISTS useful_count,
    DROP COLUMN IF EXISTS confirmation_count,
    DROP COLUMN IF EXISTS importance,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS canonical_key;
