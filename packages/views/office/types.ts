import type {
  AutonomousProjectSnapshot,
  AutonomousTeamMember,
  ProjectLeaderChangeRequest,
  Agent,
} from "@multica/core/types";

export type OfficeZoneKey =
  | "pm_desk"
  | "coding_pods"
  | "qa_lab"
  | "approval_lounge";

export type AgentCategory =
  | "management"
  | "architecture"
  | "frontend"
  | "mobile"
  | "backend"
  | "qa"
  | "security"
  | "general";

export type AgentCharacterType =
  | "lion_lead"
  | "owl_architect"
  | "cat_mobile"
  | "cat_frontend"
  | "raccoon_backend"
  | "panda_qa"
  | "fox_security"
  | "robot_general";

export type AgentDeskStatus =
  | "idle"
  | "running"
  | "debugging"
  | "verifying"
  | "blocked"
  | "failed"
  | "approved";

export interface AgentTaskItem {
  id: string;
  title: string;
  status: "running" | "in_progress" | "pending" | "ready" | "blocked" | "completed" | "in_review" | string;
  issueId?: string;
  isCurrent?: boolean;
}

export interface ActiveToolCall {
  name: string;
  target?: string;
  status: "running" | "completed";
  startedAt?: number;
}

export interface ActiveSkillInfo {
  name: string;
}

export interface DeskFurniture {
  dualMonitors?: boolean;
  verticalScreen?: boolean;
  coffeeMug?: boolean;
  serverRack?: boolean;
  chairColor?: string;
  decorTheme?: "engineering" | "architecture" | "qa" | "executive";
}

export interface InOfficeSpeechBubble {
  id: string;
  deskId: string;
  agentName: string;
  text: string;
  createdAt: number;
  expiresAt: number;
  kind?: "thought" | "announcement" | "qa";
}

export interface OfficeStationMetric {
  labelKey: string;
  value: string | number;
}

export interface OfficeStation {
  key: OfficeZoneKey;
  titleKey: string;
  x: number;
  y: number;
  width: number;
  height: number;
  iconName: "FolderKanban" | "Code" | "CheckCircle2" | "ShieldAlert";
  metrics: OfficeStationMetric[];
  roomTheme?: "engineering" | "architecture" | "qa" | "executive";
}

export interface OfficeAgentDesk {
  id: string;
  agentId?: string;
  agentName: string;
  role: string;
  avatarUrl?: string | null;
  status: AgentDeskStatus;
  currentTaskTitle?: string;
  currentIssueId?: string;
  activeTask?: AgentTaskItem;
  queuedTasks: AgentTaskItem[];
  completedTasksCount: number;
  totalAssignedTasksCount: number;
  stationKey: OfficeZoneKey;
  deskCoord: { x: number; y: number };
  rawMember?: AutonomousTeamMember;
  rawAgent?: Agent;

  // Living Office & Specialized Visuals
  category?: AgentCategory;
  categoryColor?: string;
  characterType?: AgentCharacterType;
  isLeader?: boolean;
  isDaemonActive?: boolean;

  // Tooling & Living Atmosphere
  activeTool?: ActiveToolCall;
  recentTools?: ActiveToolCall[];
  allowedSkills?: string[];
  activeSkill?: ActiveSkillInfo;
  activeSpeechBubble?: InOfficeSpeechBubble;
  furniture?: DeskFurniture;
}

export type OfficeFlowEdgeKind =
  | "task_dispatch"
  | "review_handoff"
  | "plan_dependency"
  | "issue_dependency"
  | "escalation";

export interface OfficeFlowEdge {
  id: string;
  kind: OfficeFlowEdgeKind;
  fromStation: OfficeZoneKey;
  toStation: OfficeZoneKey;
  fromCoord: { x: number; y: number };
  toCoord: { x: number; y: number };
  issueId?: string;
  relatedCount?: number;
  active: boolean;
}

export type OfficeBlockerKind =
  | "issue_dependency"
  | "plan_dependency"
  | "failed_run"
  | "failed_gate"
  | "blocked_agent"
  | "runtime_unavailable"
  | "approval_required";

export type OfficeBlockerAction =
  | { kind: "open_issue"; targetId: string }
  | { kind: "retry_run"; targetId: string }
  | { kind: "retry_action"; targetId: string }
  | { kind: "resolve_escalation"; targetId: string }
  | { kind: "approve_plan"; targetId: string }
  | { kind: "none" };

export interface OfficeBlocker {
  id: string;
  kind: OfficeBlockerKind;
  severity: "waiting" | "warning" | "error";
  agentId?: string;
  issueId?: string;
  runId?: string;
  dependencyIssueIds?: string[];
  reasonKey?: string;
  params?: Record<string, string | number>;
  action: OfficeBlockerAction;
}

export type OfficeActivityKind =
  | "task.dispatched"
  | "task.completed"
  | "task.failed"
  | "workflow.review_requested"
  | "workflow.reviewed"
  | "plan.approved"
  | "escalation.created";

export interface OfficeReplayEvent {
  id: string;
  timestamp: string;
  type: OfficeActivityKind;
  fromStation?: OfficeZoneKey;
  toStation?: OfficeZoneKey;
  agentId?: string;
  issueId?: string;
  params?: Record<string, string | number>;
}

export interface VirtualOfficeSceneData {
  stations: Record<OfficeZoneKey, OfficeStation>;
  desks: OfficeAgentDesk[];
  flowEdges: OfficeFlowEdge[];
  blockers: OfficeBlocker[];
  recentEvents: OfficeReplayEvent[];
  speechBubbles: InOfficeSpeechBubble[];
  stats: {
    activeAgents: number;
    runningTasks: number;
    completedTasks: number;
    pendingApprovals: number;
    blocked: number;
    failed: number;
    waiting: number;
    resumable: number;
  };
  pendingApprovalsCount: number;
}

export interface VirtualOfficeViewProps {
  projectId: string | null;
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[] | null;
  leaderSessionId?: string | null;
  leaderAgentId?: string | null;
  canControl?: boolean;
  onApprovePlan?: () => void;
  onApproveChange?: (changeRequestId: string) => void;
  onRejectChange?: (changeRequestId: string) => void;
  onResolveEscalation?: (escalationId: string) => void;
  className?: string;
}
