-- Durable project-level orchestration state for autonomous software delivery.
--
-- This migration intentionally defines tables only. Repository policy requires
-- explicit indexes to be built concurrently in separate single-statement
-- migrations. Relationships are application-owned; no foreign keys are used.
CREATE TABLE IF NOT EXISTS autonomous_project_plan (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    revision BIGINT NOT NULL,
    source_revision TEXT NOT NULL,
    planner_name TEXT NOT NULL,
    planner_model TEXT,
    goal TEXT NOT NULL,
    specification JSONB NOT NULL DEFAULT '{}'::jsonb,
    policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (revision > 0),
    CHECK (status IN ('draft', 'active', 'superseded', 'completed', 'blocked', 'cancelled'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_plan_node (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    node_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 0,
    required_role_family TEXT,
    required_capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    acceptance_criteria JSONB NOT NULL DEFAULT '[]'::jsonb,
    risk_level TEXT NOT NULL DEFAULT 'medium',
    spec_revision BIGINT NOT NULL DEFAULT 1,
    assigned_role TEXT,
    assigned_agent_id UUID,
    materialized_issue_id UUID,
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    ready_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    blocked_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (priority >= 0),
    CHECK (spec_revision > 0),
    CHECK (attempt >= 0),
    CHECK (max_attempts > 0),
    CHECK (status IN ('pending', 'ready', 'running', 'verification', 'completed', 'blocked', 'failed', 'cancelled')),
    CHECK (risk_level IN ('low', 'medium', 'high', 'critical'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_plan_edge (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    from_node_key TEXT NOT NULL,
    to_node_key TEXT NOT NULL,
    dependency_type TEXT NOT NULL DEFAULT 'hard',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (from_node_key <> to_node_key),
    CHECK (dependency_type IN ('hard', 'soft', 'artifact'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_brain_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID,
    node_id UUID,
    entry_type TEXT NOT NULL,
    subject TEXT NOT NULL,
    content JSONB NOT NULL,
    source_type TEXT NOT NULL,
    source_id TEXT,
    confidence DOUBLE PRECISION,
    revision BIGINT NOT NULL DEFAULT 1,
    superseded_by UUID,
    created_by_type TEXT NOT NULL,
    created_by_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    CHECK (revision > 0),
    CHECK (entry_type IN ('requirement', 'constraint', 'assumption', 'fact', 'architecture_decision', 'product_decision', 'risk', 'dependency', 'repository_fact', 'lesson')),
    CHECK (created_by_type IN ('system', 'agent', 'member'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_artifact (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID,
    node_id UUID,
    artifact_type TEXT NOT NULL,
    name TEXT NOT NULL,
    schema_version INTEGER NOT NULL DEFAULT 1,
    content JSONB NOT NULL,
    producer_agent_id UUID,
    immutable BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (schema_version > 0),
    CHECK (artifact_type IN ('product_spec', 'architecture', 'api_contract', 'data_model', 'test_plan', 'implementation_handoff', 'review', 'security_review', 'qa_report', 'integration_report', 'release_manifest', 'deployment_record', 'incident_report', 'postmortem', 'generic'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_quality_gate_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID NOT NULL,
    node_id UUID NOT NULL,
    gate_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    required BOOLEAN NOT NULL DEFAULT TRUE,
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempt INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (attempt >= 0),
    CHECK (status IN ('pending', 'running', 'passed', 'failed', 'skipped')),
    CHECK (gate_type IN ('build', 'lint', 'unit_test', 'integration_test', 'coverage', 'migration', 'security', 'api_compatibility', 'performance', 'acceptance', 'review'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_escalation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID,
    node_id UUID,
    category TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    severity TEXT NOT NULL DEFAULT 'medium',
    summary TEXT NOT NULL,
    context JSONB NOT NULL DEFAULT '{}'::jsonb,
    resolution JSONB,
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    CHECK (status IN ('open', 'acknowledged', 'resolved', 'cancelled')),
    CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    CHECK (category IN ('technical_failure', 'missing_credentials', 'business_decision', 'runtime_unavailable', 'budget_exceeded', 'ambiguous_requirement', 'unsafe_operation', 'external_dependency', 'approval_required'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_budget (
    project_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    token_limit BIGINT,
    runtime_seconds_limit BIGINT,
    cost_microunits_limit BIGINT,
    max_parallel_nodes INTEGER NOT NULL DEFAULT 4,
    max_total_attempts INTEGER NOT NULL DEFAULT 100,
    tokens_used BIGINT NOT NULL DEFAULT 0,
    runtime_seconds_used BIGINT NOT NULL DEFAULT 0,
    cost_microunits_used BIGINT NOT NULL DEFAULT 0,
    total_attempts INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (max_parallel_nodes > 0),
    CHECK (max_total_attempts > 0),
    CHECK (tokens_used >= 0),
    CHECK (runtime_seconds_used >= 0),
    CHECK (cost_microunits_used >= 0),
    CHECK (total_attempts >= 0)
);

CREATE TABLE IF NOT EXISTS autonomous_project_deployment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    plan_id UUID,
    environment TEXT NOT NULL,
    provider TEXT NOT NULL,
    external_ref TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    policy_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'rolled_back', 'cancelled'))
);

CREATE TABLE IF NOT EXISTS autonomous_project_incident (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    deployment_id UUID,
    severity TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    title TEXT NOT NULL,
    evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    root_cause JSONB,
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    CHECK (status IN ('open', 'investigating', 'mitigated', 'resolved', 'cancelled'))
);
