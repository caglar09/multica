ALTER TABLE autonomous_project_deployment
    DROP COLUMN IF EXISTS node_id,
    DROP COLUMN IF EXISTS generic_operation_id;
