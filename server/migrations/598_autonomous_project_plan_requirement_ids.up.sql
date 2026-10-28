-- Existing plans have no authoritative node-to-requirement mapping. Keep
-- those rows explicitly unmapped instead of deriving coverage from text.
ALTER TABLE autonomous_project_plan_node
    ADD COLUMN requirement_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE autonomous_project_plan_node
    ADD CONSTRAINT autonomous_project_plan_node_requirement_ids_array
    CHECK (jsonb_typeof(requirement_ids) = 'array');
