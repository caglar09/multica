ALTER TABLE autonomous_project_plan_node
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_node_requirement_ids_array;

ALTER TABLE autonomous_project_plan_node
    DROP COLUMN IF EXISTS requirement_ids;
