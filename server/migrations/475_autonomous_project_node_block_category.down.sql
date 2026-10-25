ALTER TABLE autonomous_project_plan_node
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_node_blocked_category_check;

ALTER TABLE autonomous_project_plan_node
    DROP COLUMN IF EXISTS blocked_category;
