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

export interface AutonomousRoleRuntimeAssignment {
  role: string;
  runtime_id: string;
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

export interface AutonomousProjectSnapshot {
  enabled: boolean;
  control: AutonomousProjectControl;
  health: AutonomousProjectHealth;
  draft: AutonomousTeamDraft | null;
  runtimes: AutonomousRuntimeOption[];
  skills: AutonomousSkillOption[];
  team: AutonomousTeam | null;
  workflows: AutonomousWorkflowRun[];
  actions: AutonomousWorkflowAction[];
  activity: AutonomousActivityItem[];
  decisions: AutonomousDecision[];
}
