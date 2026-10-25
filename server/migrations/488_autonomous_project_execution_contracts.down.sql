DROP TABLE IF EXISTS autonomous_project_change_set;

ALTER TABLE autonomous_project_escalation
    DROP CONSTRAINT IF EXISTS autonomous_project_escalation_category_check;
ALTER TABLE autonomous_project_escalation
    ADD CONSTRAINT autonomous_project_escalation_category_check
    CHECK (category IN ('technical_failure','missing_credentials','business_decision','runtime_unavailable','budget_exceeded','ambiguous_requirement','unsafe_operation','external_dependency','approval_required'));

ALTER TABLE autonomous_project_plan_node
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_node_blocked_category_check;
ALTER TABLE autonomous_project_plan_node
    ADD CONSTRAINT autonomous_project_plan_node_blocked_category_check
    CHECK (blocked_category IS NULL OR blocked_category IN ('dependency','approval','external_dependency','technical_failure','budget','manual'));

ALTER TABLE autonomous_project_plan_edge
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_edge_artifact_contract_check;
ALTER TABLE autonomous_project_plan_edge
    DROP COLUMN IF EXISTS required_artifact_type;
