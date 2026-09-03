export type AutonomousHealthStatus = "idle" | "ready" | "running" | "paused" | "attention";

export interface AutonomousProjectControl {
  paused: boolean;
  paused_at: string | null;
  replan_requested_at: string | null;
  replan_completed_at: string | null;
  last_error: string | null;
}

export interface AutonomousTeamMember {
  role: string;
  family: string;
  agent_id: string;
  agent_name: string;
  capabilities: string[];
  responsibilities: string[];
  reason: string;
  active: boolean;
  current_task_id: string | null;
  current_task_title: string | null;
  current_task_status: string | null;
  created_at: string;
}

export interface AutonomousTeam {
  id: string;
  squad_id: string;
  intent: string;
  status: string;
  planner_name: string;
  planner_model: string | null;
  plan_revision: number;
  last_planned_at: string | null;
  updated_at: string;
  members: AutonomousTeamMember[];
}

export interface AutonomousWorkflowAction {
  id: string;
  run_id: string;
  action_type: string;
  status: string;
  attempts: number;
  max_attempts: number;
  last_error: string | null;
  created_at: string;
  updated_at: string;
}

export interface AutonomousWorkflowRun {
  id: string;
  issue_id: string;
  issue_title: string;
  state: string;
  revision: number;
  review_cycles: number;
  owner_agent_id: string | null;
  owner_agent_name: string | null;
  reviewer_agent_id: string | null;
  reviewer_agent_name: string | null;
  pending_actions: number;
  failed_actions: number;
  updated_at: string;
}

export interface AutonomousActivityItem {
  id: string;
  type: string;
  title: string;
  detail?: string;
  issue_id?: string;
  agent_id?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface AutonomousTeamPlanRole {
  role: string;
  family: string;
  display_name: string;
  capabilities?: string[];
  responsibilities?: string[];
  reason?: string;
}

export interface AutonomousTeamPlan {
  version?: number;
  intent?: string;
  summary?: string;
  implementation_role?: string;
  route_role?: string;
  planner_name?: string;
  planner_model?: string;
  roles?: AutonomousTeamPlanRole[];
}

export interface AutonomousProjectBootstrap {
  autonomy_mode: "standard" | "autonomous";
  autonomy_level: "assisted" | "development" | "delivery" | "closed_loop";
  brief: string;
  knowledge: Array<{
    kind: string;
    title: string;
    content: string;
  }>;
  policy: Record<string, unknown>;
  budget: Record<string, unknown>;
  status: string;
  updated_at: string;
}

export interface AutonomousTeamDraft {
  status: "awaiting_configuration" | "provisioning";
  planner_name: string;
  planner_model: string | null;
  plan: AutonomousTeamPlan;
  default_runtime_id: string | null;
  default_skill_ids: string[];
  created_at: string;
  updated_at: string;
}

export interface AutonomousRuntimeOption {
  id: string;
  name: string;
  provider: string;
  runtime_mode: string;
  status: string;
}

export interface AutonomousSkillOption {
  id: string;
  name: string;
  description: string;
}

export type AutonomousSkillMode = "inherit" | "custom";

export interface AutonomousRoleRuntimeAssignment {
  role: string;
  runtime_id: string;
  model?: string;
  skill_mode: AutonomousSkillMode;
  skill_ids: string[];
}

export interface AutonomousDecision {
  id: string;
  source_type: string;
  source_id: string;
  source_revision: string;
  planner_name: string;
  planner_model: string | null;
  plan: AutonomousTeamPlan;
  created_at: string;
}

export interface AutonomousProjectHealth {
  status: AutonomousHealthStatus;
  active_workflows: number;
  blocked: number;
  failed_actions: number;
}

export type AutonomousBrainRuntimeMode = "inherit_mika" | "custom";
export type AutonomousBrainLearningMode = "deterministic" | "assisted" | "adaptive";

export interface AutonomousBrainConfig {
  enabled: boolean;
  runtime_mode: AutonomousBrainRuntimeMode;
  runtime_id: string | null;
  model: string | null;
  thinking_level: string | null;
  service_tier: string | null;
  learning_mode: AutonomousBrainLearningMode;
  active_memories: number;
  superseded_memories: number;
  pending_learning_jobs: number;
  deferred_learning_jobs: number;
}

export interface UpdateAutonomousBrainConfig {
  enabled: boolean;
  runtime_mode: AutonomousBrainRuntimeMode;
  runtime_id?: string;
  model?: string;
  thinking_level?: string;
  service_tier?: string;
  learning_mode: AutonomousBrainLearningMode;
}


export interface AutonomousProjectPlanNode {
  id: string;
  key: string;
  kind: string;
  title: string;
  status: string;
  priority: number;
  risk: string;
  required_role_family: string | null;
  assigned_role: string | null;
  assigned_agent_id: string | null;
  materialized_issue_id: string | null;
  attempt: number;
  max_attempts: number;
  acceptance_criteria: string[];
  updated_at: string;
}

export interface AutonomousProjectPlanEdge {
  from: string;
  to: string;
  type: "hard" | "soft" | "artifact" | string;
}

export interface AutonomousProjectPlanSnapshot {
  id: string;
  revision: number;
  goal: string;
  status: string;
  planner_name: string;
  planner_model: string | null;
  specification: {
    summary?: string;
    requirements?: string[];
    non_functional_requirements?: string[];
    constraints?: string[];
    definition_of_done?: string[];
    [key: string]: unknown;
  };
  policy: Record<string, unknown>;
  nodes: AutonomousProjectPlanNode[];
  edges: AutonomousProjectPlanEdge[];
  updated_at: string;
}

export interface AutonomousQualityGate {
  id: string;
  node_id: string;
  gate_type: string;
  status: string;
  required: boolean;
  evidence: Record<string, unknown>;
  last_error: string | null;
  updated_at: string;
}

export interface AutonomousEscalation {
  id: string;
  node_id: string | null;
  category: string;
  status: string;
  severity: string;
  summary: string;
  context: Record<string, unknown>;
  resolution?: Record<string, unknown>;
  opened_at: string;
  resolved_at: string | null;
}

export interface AutonomousBudget {
  token_limit: number | null;
  runtime_seconds_limit: number | null;
  cost_microunits_limit: number | null;
  max_parallel_nodes: number;
  max_total_attempts: number;
  tokens_used: number;
  runtime_seconds_used: number;
  cost_microunits_used: number;
  total_attempts: number;
}

export interface AutonomousProjectSnapshot {
  enabled: boolean;
  control: AutonomousProjectControl;
  health: AutonomousProjectHealth;
  bootstrap: AutonomousProjectBootstrap | null;
  draft: AutonomousTeamDraft | null;
  runtimes: AutonomousRuntimeOption[];
  skills: AutonomousSkillOption[];
  team: AutonomousTeam | null;
  workflows: AutonomousWorkflowRun[];
  actions: AutonomousWorkflowAction[];
  activity: AutonomousActivityItem[];
  decisions: AutonomousDecision[];
  plan: AutonomousProjectPlanSnapshot | null;
  quality_gates: AutonomousQualityGate[];
  escalations: AutonomousEscalation[];
  budget: AutonomousBudget | null;
  brain: AutonomousBrainConfig | null;
}
