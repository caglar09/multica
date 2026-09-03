-- Phase 2: deterministic artifact contracts and durable ChangeSet lifecycle.
ALTER TABLE autonomous_project_plan_edge
    ADD COLUMN IF NOT EXISTS required_artifact_type TEXT;

UPDATE autonomous_project_plan_edge e
SET required_artifact_type = CASE n.kind
    WHEN 'product' THEN 'product_spec'
    WHEN 'architecture' THEN 'architecture'
    WHEN 'review' THEN 'review'
    WHEN 'qa' THEN 'qa_report'
    WHEN 'security' THEN 'security_review'
    WHEN 'integration' THEN 'integration_report'
    WHEN 'release' THEN 'release_manifest'
    WHEN 'deploy' THEN 'deployment_record'
    WHEN 'incident' THEN 'incident_report'
    ELSE 'implementation_handoff'
END
FROM autonomous_project_plan_node n
WHERE e.plan_id = n.plan_id
  AND e.from_node_key = n.node_key
  AND e.dependency_type = 'artifact'
  AND e.required_artifact_type IS NULL;

ALTER TABLE autonomous_project_plan_edge
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_edge_artifact_contract_check;
ALTER TABLE autonomous_project_plan_edge
    ADD CONSTRAINT autonomous_project_plan_edge_artifact_contract_check
    CHECK (
        (dependency_type = 'artifact' AND required_artifact_type IS NOT NULL AND btrim(required_artifact_type) <> '')
        OR (dependency_type <> 'artifact' AND required_artifact_type IS NULL)
    );

-- Legacy artifacts are kept for audit but cannot silently satisfy Phase 2
-- contracts because no deterministic validation was recorded when they were
-- produced.
UPDATE autonomous_project_artifact
SET content = jsonb_set(
    content,
    '{contract}',
    jsonb_build_object(
        'status', 'invalid',
        'valid', false,
        'spec_revision', COALESCE((
            SELECT n.spec_revision
            FROM autonomous_project_plan_node n
            WHERE n.id = autonomous_project_artifact.node_id
        ), 0),
        'validation_error', 'legacy artifact predates Phase 2 contract validation'
    ),
    true
)
WHERE NOT (content ? 'contract');

ALTER TABLE autonomous_project_plan_node
    DROP CONSTRAINT IF EXISTS autonomous_project_plan_node_blocked_category_check;
ALTER TABLE autonomous_project_plan_node
    ADD CONSTRAINT autonomous_project_plan_node_blocked_category_check
    CHECK (
        blocked_category IS NULL
        OR blocked_category IN (
            'dependency','approval','external_dependency','technical_failure',
            'budget','manual','no_eligible_agent','merge_conflict','stale_base','quality_policy'
        )
    );

ALTER TABLE autonomous_project_escalation
    DROP CONSTRAINT IF EXISTS autonomous_project_escalation_category_check;
ALTER TABLE autonomous_project_escalation
    ADD CONSTRAINT autonomous_project_escalation_category_check
    CHECK (category IN (
        'technical_failure','missing_credentials','business_decision','runtime_unavailable',
        'budget_exceeded','ambiguous_requirement','unsafe_operation','external_dependency',
        'approval_required','contract_violation','no_eligible_agent','merge_conflict',
        'stale_base','quality_policy'
    ));

CREATE TABLE autonomous_project_change_set (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID NOT NULL,
    node_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    source_task_id UUID NOT NULL UNIQUE,
    source_agent_id UUID NOT NULL,
    runtime_id UUID,
    daemon_id TEXT,
    base_sha TEXT NOT NULL,
    branch_name TEXT NOT NULL,
    worktree_path TEXT,
    repo_path TEXT,
    changed_files JSONB NOT NULL DEFAULT '[]'::jsonb,
    commit_sha TEXT NOT NULL,
    merge_status TEXT NOT NULL DEFAULT 'ready',
    merge_order BIGINT,
    integration_branch TEXT,
    merged_sha TEXT,
    approved_review_task_id UUID,
    merge_evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ready_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    queued_at TIMESTAMPTZ,
    merged_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (base_sha <> ''),
    CHECK (branch_name <> ''),
    CHECK (commit_sha <> ''),
    CHECK (merge_order IS NULL OR merge_order > 0),
    CHECK (merge_status IN (
        'ready','changes_requested','queued','merging','merged',
        'conflict','stale_base','integration_failed','cancelled'
    ))
);
