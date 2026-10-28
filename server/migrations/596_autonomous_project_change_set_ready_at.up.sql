ALTER TABLE autonomous_project_change_set
    ALTER COLUMN ready_at DROP NOT NULL,
    ALTER COLUMN ready_at DROP DEFAULT;
