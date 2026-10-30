import type {
  AutonomousProjectSnapshot,
  AutonomousTeamMember,
  ProjectLeaderChangeRequest,
  Agent,
  Issue,
} from "@multica/core/types";

export type OfficeZoneKey =
  | "pm_desk"
  | "coding_pods"
  | "qa_lab"
  | "approval_lounge";

export type AgentDeskStatus =
  | "idle"
  | "running"
  | "debugging"
  | "verifying"
  | "blocked"
  | "approved";

export interface AgentTaskItem {
  id: string;
  title: string;
  status: "running" | "in_progress" | "pending" | "ready" | "blocked" | "completed" | "in_review" | string;
  issueId?: string;
  isCurrent?: boolean;
}

export interface OfficeStationMetric {
  label: string;
  value: string | number;
}

export interface OfficeStation {
  key: OfficeZoneKey;
  titleKey: string;
  defaultTitle: string;
  description: string;
  x: number;
  y: number;
  width: number;
  height: number;
  statusColor: string;
  iconName: "FolderKanban" | "Code" | "CheckCircle2" | "ShieldAlert";
  metrics: OfficeStationMetric[];
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
}

export interface OfficeFlowEdge {
  id: string;
  fromStation: OfficeZoneKey;
  toStation: OfficeZoneKey;
  fromCoord: { x: number; y: number };
  toCoord: { x: number; y: number };
  color: string;
  label?: string;
  issueId?: string;
  active: boolean;
  pulseSpeed?: "normal" | "fast";
}

export interface OfficeReplayEvent {
  id: string;
  title: string;
  timestamp: string;
  type: string;
  fromStation: OfficeZoneKey;
  toStation: OfficeZoneKey;
  issueId?: string;
  agentName?: string;
  payloadSummary?: string;
}

export interface VirtualOfficeSceneData {
  stations: Record<OfficeZoneKey, OfficeStation>;
  desks: OfficeAgentDesk[];
  flowEdges: OfficeFlowEdge[];
  recentEvents: OfficeReplayEvent[];
  stats: {
    activeAgents: number;
    runningTasks: number;
    completedTasks: number;
    pendingApprovals: number;
  };
  pendingApprovalsCount: number;
}

export interface VirtualOfficeViewProps {
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[] | null;
  agents?: Agent[];
  issues?: Issue[];
  canControl?: boolean;
  onApprovePlan?: () => void;
  onApproveChange?: (changeRequestId: string) => void;
  onRejectChange?: (changeRequestId: string) => void;
  onResolveEscalation?: (escalationId: string) => void;
  onOpenLeaderChat?: () => void;
  className?: string;
}
