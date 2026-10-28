UPDATE autonomous_project_change_set
SET ready_at = created_at
WHERE ready_at IS NULL;

ALTER TABLE autonomous_project_change_set
    ALTER COLUMN ready_at SET DEFAULT now(),
    ALTER COLUMN ready_at SET NOT NULL;
