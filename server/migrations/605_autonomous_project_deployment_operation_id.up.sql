ALTER TABLE autonomous_project_deployment
    ADD COLUMN IF NOT EXISTS generic_operation_id UUID,
    ADD COLUMN IF NOT EXISTS node_id UUID;
