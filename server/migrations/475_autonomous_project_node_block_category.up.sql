ALTER TABLE autonomous_project_plan_node
    ADD COLUMN IF NOT EXISTS blocked_category TEXT;

ALTER TABLE autonomous_project_plan_node
    ADD CONSTRAINT autonomous_project_plan_node_blocked_category_check
    CHECK (
        blocked_category IS NULL
        OR blocked_category IN (
            'dependency',
            'approval',
            'external_dependency',
            'technical_failure',
            'budget',
            'manual'
        )
    );
