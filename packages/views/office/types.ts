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
  title?: string;
  category?: string;
  description?: string;
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

export interface OfficeChatMessage {
  id: string;
  sender: "user" | "pm" | "ceo" | "agent";
  senderName: string;
  targetAgentId?: string;
  targetAgentName?: string;
  text: string;
  timestamp: string;
  relatedTaskId?: string;
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
  categoryLabel?: string;
  categoryColor?: string;
  characterType?: AgentCharacterType;
  isLeader?: boolean;
  isDaemonActive?: boolean;
  isProcessingInquiry?: boolean;

  // Tooling & Living Atmosphere
  activeTool?: ActiveToolCall;
  recentTools?: ActiveToolCall[];
  allowedSkills?: string[];
  activeSkill?: ActiveSkillInfo;
  activeSpeechBubble?: InOfficeSpeechBubble;
  furniture?: DeskFurniture;
  tokenUsageMode?: "zero_token_idle" | "active_running" | "verifying";
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
  speechBubbles: InOfficeSpeechBubble[];
  chatHistory: OfficeChatMessage[];
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
  onSendChatMessage?: (message: { targetAgentId?: string; text: string }) => Promise<void> | void;
  className?: string;
}
