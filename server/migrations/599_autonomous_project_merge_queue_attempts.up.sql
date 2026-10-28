ALTER TABLE autonomous_project_change_set
    ADD COLUMN merge_attempt INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_error TEXT,
    ADD CONSTRAINT autonomous_project_change_set_merge_attempt_check
        CHECK (merge_attempt >= 0);
