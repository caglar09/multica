import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  Agent,
  Issue,
} from "@multica/core/types";
import { filterSkillsForRole } from "@multica/core/skills";
import type {
  OfficeZoneKey,
  OfficeStation,
  OfficeAgentDesk,
  OfficeFlowEdge,
  OfficeReplayEvent,
  VirtualOfficeSceneData,
  AgentDeskStatus,
  ActiveToolCall,
  InOfficeSpeechBubble,
  OfficeChatMessage,
  AgentCategory,
  AgentCharacterType,
} from "./types";

export interface BuildVirtualOfficeParams {
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[] | null;
  agents?: Agent[];
  issues?: Issue[];
  availableSkills?: string[];
  chatHistory?: OfficeChatMessage[];
}

export function resolveAgentCategoryAndCharacter(
  role: string,
  isLeader = false,
): {
  category: AgentCategory;
  categoryLabel: string;
  categoryColor: string;
  characterType: AgentCharacterType;
} {
  const r = role.toLowerCase();
  if (
    isLeader ||
    r.includes("leader") ||
    r.includes("project_manager") ||
    r.includes("pm") ||
    r.includes("product_manager") ||
    r.includes("yönetici") ||
    r.includes("lider")
  ) {
    return {
      category: "management",
      categoryLabel: "Proje Yöneticisi & Lider",
      categoryColor: "#f59e0b", // Amber / Gold
      characterType: "lion_lead",
    };
  }
  if (
    r.includes("architect") ||
    r.includes("solution") ||
    r.includes("system") ||
    r.includes("mimar")
  ) {
    return {
      category: "architecture",
      categoryLabel: "Sistem & Çözüm Mimarisi",
      categoryColor: "#0ea5e9", // Sky Blue
      characterType: "owl_architect",
    };
  }
  if (
    r.includes("mobile") ||
    r.includes("android") ||
    r.includes("ios") ||
    r.includes("react-native") ||
    r.includes("flutter")
  ) {
    return {
      category: "mobile",
      categoryLabel: "Mobil Geliştirme",
      categoryColor: "#ec4899", // Pink
      characterType: "cat_mobile",
    };
  }
  if (
    r.includes("front") ||
    r.includes("ui") ||
    r.includes("web") ||
    r.includes("design") ||
    r.includes("css")
  ) {
    return {
      category: "frontend",
      categoryLabel: "Frontend & UI Mühendisliği",
      categoryColor: "#06b6d4", // Cyan
      characterType: "cat_frontend",
    };
  }
  if (
    r.includes("back") ||
    r.includes("api") ||
    r.includes("server") ||
    r.includes("go") ||
    r.includes("sql") ||
    r.includes("database")
  ) {
    return {
      category: "backend",
      categoryLabel: "Backend & Servis Çekirdeği",
      categoryColor: "#8b5cf6", // Purple
      characterType: "raccoon_backend",
    };
  }
  if (
    r.includes("qa") ||
    r.includes("test") ||
    r.includes("review") ||
    r.includes("quality") ||
    r.includes("doğrulama")
  ) {
    return {
      category: "qa",
      categoryLabel: "Test & Kalite Güvencesi",
      categoryColor: "#10b981", // Emerald
      characterType: "panda_qa",
    };
  }
  if (
    r.includes("sec") ||
    r.includes("guard") ||
    r.includes("privacy") ||
    r.includes("güvenlik")
  ) {
    return {
      category: "security",
      categoryLabel: "Güvenlik & Gizlilik",
      categoryColor: "#ef4444", // Red
      characterType: "fox_security",
    };
  }
  return {
    category: "general",
    categoryLabel: "Yazılım Mühendisliği",
    categoryColor: "#6366f1", // Indigo
    characterType: "robot_general",
  };
}

export const STATION_COORDINATES: Record<
  OfficeZoneKey,
  { x: number; y: number; width: number; height: number }
> = {
  pm_desk: { x: 600, y: 140, width: 280, height: 160 },
  coding_pods: { x: 380, y: 480, width: 560, height: 320 },
  qa_lab: { x: 920, y: 480, width: 420, height: 320 },
  approval_lounge: { x: 1050, y: 140, width: 280, height: 160 },
};

export function buildVirtualOfficeScene({
  snapshot,
  changeRequests = [],
  agents = [],
  issues = [],
  availableSkills = [
    "architecture",
    "senior-architect",
    "ultrathink",
    "frontend-ui-engineering",
    "design-system",
    "shadcn-ui",
    "react-doctor",
    "mobile",
    "react-native-best-practices",
    "react-native-animations",
    "backend",
    "drizzle-orm",
    "prisma-cli",
    "debugging",
    "testing",
    "unlazy",
    "codebase-design",
    "code-mentor",
    "dependency-management",
  ],
  chatHistory = [],
}: BuildVirtualOfficeParams): VirtualOfficeSceneData {
  const pendingChangeRequests = (changeRequests ?? []).filter(
    (cr) => cr.state === "pending",
  );
  const planAwaitingApproval = snapshot?.plan?.status === "awaiting_approval";
  const pendingApprovalsCount =
    (planAwaitingApproval ? 1 : 0) +
    pendingChangeRequests.length +
    (snapshot?.escalations?.filter((e) => e.status === "pending")?.length ?? 0);

  const completedNodesCount =
    snapshot?.plan?.nodes?.filter((n) => n.status === "completed")?.length ?? 0;
  const runningWorkflows =
    snapshot?.workflows?.filter((w) => w.state === "running") ?? [];
  const inReviewWorkflows =
    snapshot?.workflows?.filter(
      (w) => w.state === "reviewing" || Boolean(w.reviewer_agent_id),
    ) ?? [];

  // 1. Build 4 Stations with Real Office Theming
  const stations: Record<OfficeZoneKey, OfficeStation> = {
    pm_desk: {
      key: "pm_desk",
      titleKey: "office.zones.pm_desk",
      defaultTitle: "Planning & Architecture Suite",
      description: "Strategy, Roadmap Breakdown & Autonomous Coordination",
      x: STATION_COORDINATES.pm_desk.x,
      y: STATION_COORDINATES.pm_desk.y,
      width: STATION_COORDINATES.pm_desk.width,
      height: STATION_COORDINATES.pm_desk.height,
      statusColor: "#0ea5e9", // Sky Blue
      iconName: "FolderKanban",
      roomTheme: "architecture",
      metrics: [
        {
          label: "Leader",
          value: snapshot?.team?.leader_name || "PM Agent",
        },
        {
          label: "Revision",
          value: `v${snapshot?.plan?.revision ?? 1}`,
        },
        {
          label: "Nodes",
          value: snapshot?.plan?.nodes?.length ?? 0,
        },
      ],
    },
    coding_pods: {
      key: "coding_pods",
      titleKey: "office.zones.coding_pods",
      defaultTitle: "Engineering & Dev Studio",
      description: "Parallel Dual-Monitor Workbenches & Feature Implementation",
      x: STATION_COORDINATES.coding_pods.x,
      y: STATION_COORDINATES.coding_pods.y,
      width: STATION_COORDINATES.coding_pods.width,
      height: STATION_COORDINATES.coding_pods.height,
      statusColor: "#6366f1", // Indigo
      iconName: "Code",
      roomTheme: "engineering",
      metrics: [
        {
          label: "Active Pods",
          value: snapshot?.team?.members?.length ?? Math.max(agents.length, 1),
        },
        {
          label: "Running",
          value: runningWorkflows.length,
        },
      ],
    },
    qa_lab: {
      key: "qa_lab",
      titleKey: "office.zones.qa_lab",
      defaultTitle: "QA & Verification Lab",
      description: "Security Gates, Test Runner & Peer Code Review",
      x: STATION_COORDINATES.qa_lab.x,
      y: STATION_COORDINATES.qa_lab.y,
      width: STATION_COORDINATES.qa_lab.width,
      height: STATION_COORDINATES.qa_lab.height,
      statusColor: "#a855f7", // Purple
      iconName: "CheckCircle2",
      roomTheme: "qa",
      metrics: [
        {
          label: "Gates",
          value: snapshot?.quality_gates?.length ?? 0,
        },
        {
          label: "Reviews",
          value: inReviewWorkflows.length,
        },
      ],
    },
    approval_lounge: {
      key: "approval_lounge",
      titleKey: "office.zones.approval_lounge",
      defaultTitle: "Executive & Review Lounge",
      description: "Human-in-the-Loop Decision Gate, Approvals & Completed Goals",
      x: STATION_COORDINATES.approval_lounge.x,
      y: STATION_COORDINATES.approval_lounge.y,
      width: STATION_COORDINATES.approval_lounge.width,
      height: STATION_COORDINATES.approval_lounge.height,
      statusColor: pendingApprovalsCount > 0 ? "#f59e0b" : "#10b981", // Amber or Emerald
      iconName: "ShieldAlert",
      roomTheme: "executive",
      metrics: [
        {
          label: "Pending",
          value: pendingApprovalsCount,
        },
        {
          label: "Approved",
          value: completedNodesCount,
        },
      ],
    },
  };

function determineDeskToolAndSkills(
  role: string,
  status: AgentDeskStatus,
  activeTaskTitle: string | undefined,
  availableSkills: string[],
  stationKey: OfficeZoneKey,
  index: number
): {
  allowedSkills: string[];
  activeSkill?: { name: string; title: string; category: string; description: string };
  activeTool?: ActiveToolCall;
  furniture: {
    dualMonitors: boolean;
    verticalScreen: boolean;
    coffeeMug: boolean;
    serverRack: boolean;
    decorTheme: "engineering" | "architecture" | "qa" | "executive";
  };
  tokenUsageMode: "zero_token_idle" | "active_running" | "verifying";
  activeSpeechBubble?: InOfficeSpeechBubble;
} {
  const { allowedSkills, profile } = filterSkillsForRole(role, availableSkills);
  const isWorking = status === "running" || status === "debugging";
  const isVerifying = status === "verifying";

  // Furniture assignment
  const decorTheme: "engineering" | "architecture" | "qa" | "executive" =
    stationKey === "coding_pods"
      ? "engineering"
      : stationKey === "qa_lab"
        ? "qa"
        : stationKey === "pm_desk"
          ? "architecture"
          : "executive";

  const furniture = {
    dualMonitors: decorTheme !== "executive",
    verticalScreen: decorTheme === "engineering" && index % 2 === 0,
    coffeeMug: true,
    serverRack: decorTheme === "engineering" || decorTheme === "qa",
    decorTheme,
  };

  // Token usage mode
  const tokenUsageMode = isWorking
    ? "active_running"
    : isVerifying
      ? "verifying"
      : "zero_token_idle";

  // Active Tool Call simulation / extraction
  let activeTool: ActiveToolCall | undefined = undefined;
  if (isWorking) {
    const roleCat = profile.roleId;
    if (roleCat === "qa") {
      activeTool = { name: "Bash", target: "pnpm test --run", status: "running", startedAt: Date.now() - 9000 };
    } else if (roleCat === "mobile") {
      activeTool = { name: "Read", target: "apps/mobile/src/navigation.tsx", status: "running", startedAt: Date.now() - 5000 };
    } else if (roleCat === "frontend") {
      activeTool = { name: "Edit", target: "packages/views/office/canvas.tsx", status: "running", startedAt: Date.now() - 4000 };
    } else if (roleCat === "backend") {
      activeTool = { name: "Grep", target: "server/internal/handler/*.go", status: "running", startedAt: Date.now() - 12000 };
    } else if (roleCat === "lead" || roleCat === "architecture") {
      activeTool = { name: "Plan", target: "roadmap-decomposition", status: "running", startedAt: Date.now() - 15000 };
    } else {
      activeTool = { name: "Edit", target: "src/worker.ts", status: "running", startedAt: Date.now() - 3000 };
    }
  } else if (isVerifying) {
    activeTool = { name: "Verify", target: "quality-gates --audit", status: "running", startedAt: Date.now() - 6000 };
  }

  // Active Skill
  const recommended = profile.recommendedSkills[0] || allowedSkills[0];
  const activeSkill = recommended
    ? {
        name: recommended,
        title: profile.title,
        category: profile.roleId,
        description: profile.description,
      }
    : undefined;

  // Active Speech Bubble
  let activeSpeechBubble: InOfficeSpeechBubble | undefined = undefined;
  if (isWorking && activeTaskTitle) {
    activeSpeechBubble = {
      id: `bubble-${role}-${Date.now()}`,
      deskId: `desk-${role}`,
      agentName: profile.title,
      text: `Çalışıyor: ${activeTaskTitle.slice(0, 48)}...`,
      createdAt: Date.now(),
      expiresAt: Date.now() + 60000,
      kind: "thought",
    };
  } else if (status === "idle") {
    activeSpeechBubble = {
      id: `bubble-idle-${role}-${Date.now()}`,
      deskId: `desk-${role}`,
      agentName: profile.title,
      text: "Boşta • 0-Token Standby",
      createdAt: Date.now(),
      expiresAt: Date.now() + 60000,
      kind: "thought",
    };
  } else if (isVerifying) {
    activeSpeechBubble = {
      id: `bubble-verif-${role}-${Date.now()}`,
      deskId: `desk-${role}`,
      agentName: profile.title,
      text: "Test ve kalite doğrulaması yapılıyor...",
      createdAt: Date.now(),
      expiresAt: Date.now() + 60000,
      kind: "qa",
    };
  }

  return {
    allowedSkills,
    activeSkill,
    activeTool,
    furniture,
    tokenUsageMode,
    activeSpeechBubble,
  };
}

  // 2. Build Agent Desks
  const desks: OfficeAgentDesk[] = [];
  const agentsMap = new Map<string, Agent>();
  for (const a of agents) {
    agentsMap.set(a.id, a);
  }

  const allPlanNodes = snapshot?.plan?.nodes ?? [];
  const allWorkflows = snapshot?.workflows ?? [];

  // Leader Desk at PM Station
  if (snapshot?.team?.leader_agent_id || agents.length > 0) {
    const leaderId = snapshot?.team?.leader_agent_id ?? agents[0]?.id;
    const leaderAgent = leaderId ? agentsMap.get(leaderId) : undefined;
    const leaderRole =
      snapshot?.team?.leader_name || leaderAgent?.name || "Project Leader (PM)";
    const leaderStatus: AgentDeskStatus = planAwaitingApproval
      ? "verifying"
      : snapshot?.bootstrap?.status === "running"
        ? "running"
        : "idle";

    const leaderPlanNodes = allPlanNodes.filter(
      (n) => n.assigned_agent_id === leaderId || n.is_epic,
    );
    const activeLeaderNode = leaderPlanNodes.find(
      (n) => n.status === "in_progress" || n.status === "running",
    );
    const queuedLeaderNodes = leaderPlanNodes.filter(
      (n) => n.id !== activeLeaderNode?.id && ["pending", "ready", "blocked"].includes(n.status),
    );

    const leaderVisual = resolveAgentCategoryAndCharacter(leaderRole, true);

    const leaderMeta = determineDeskToolAndSkills(
      leaderRole,
      leaderStatus,
      activeLeaderNode?.title || snapshot?.plan?.goal || undefined,
      availableSkills,
      "pm_desk",
      0,
    );

    desks.push({
      id: `desk-pm-${leaderId ?? "leader"}`,
      agentId: leaderId,
      agentName: leaderAgent?.name || snapshot?.team?.leader_name || "Leader Agent",
      role: leaderRole,
      avatarUrl: leaderAgent?.avatar_url,
      status: leaderStatus,
      currentTaskTitle: planAwaitingApproval
        ? "Waiting for plan approval"
        : snapshot?.plan?.goal || "Strategic Roadmap Coordination",
      activeTask: activeLeaderNode
        ? {
            id: activeLeaderNode.id,
            title: activeLeaderNode.title,
            status: activeLeaderNode.status,
            issueId: activeLeaderNode.materialized_issue_id || undefined,
            isCurrent: true,
          }
        : undefined,
      queuedTasks: queuedLeaderNodes.map((n) => ({
        id: n.id,
        title: n.title,
        status: n.status,
        issueId: n.materialized_issue_id || undefined,
        isCurrent: false,
      })),
      completedTasksCount: leaderPlanNodes.filter((n) => n.status === "completed").length,
      totalAssignedTasksCount:
        (activeLeaderNode ? 1 : 0) +
        queuedLeaderNodes.length +
        leaderPlanNodes.filter((n) => n.status === "completed").length,
      stationKey: "pm_desk",
      deskCoord: {
        x: STATION_COORDINATES.pm_desk.x,
        y: STATION_COORDINATES.pm_desk.y,
      },
      isLeader: true,
      category: leaderVisual.category,
      categoryLabel: leaderVisual.categoryLabel,
      categoryColor: leaderVisual.categoryColor,
      characterType: leaderVisual.characterType,
      rawAgent: leaderAgent,
      ...leaderMeta,
    });
  }

  // Team Members
  const members = snapshot?.team?.members ?? [];
  if (members.length > 0) {
    const memberCount = members.length;
    const row1Count = memberCount <= 3 ? memberCount : Math.ceil(memberCount / 2);
    const row2Count = memberCount <= 3 ? 0 : memberCount - row1Count;
    const horizontalSpacing = 340;

    for (let index = 0; index < memberCount; index++) {
      const member = members[index];
      if (!member) continue;
      const isRow2 = index >= row1Count;
      const indexInRow = isRow2 ? index - row1Count : index;
      const countInCurrentRow = isRow2 ? row2Count : row1Count;

      const startX = 600 - ((countInCurrentRow - 1) * horizontalSpacing) / 2;
      const deskX = startX + indexInRow * horizontalSpacing;
      const deskY = isRow2 ? 650 : 410;

      const agent = agentsMap.get(member.agent_id);
      const isQaRole =
        member.role.toLowerCase().includes("qa") ||
        member.role.toLowerCase().includes("tester") ||
        member.role.toLowerCase().includes("reviewer") ||
        member.family === "qa";

      const workflow = allWorkflows.find(
        (w) => w.owner_agent_id === member.agent_id || w.reviewer_agent_id === member.agent_id,
      );
      let status: AgentDeskStatus = "idle";
      if (workflow) {
        if (workflow.state === "running") status = "running";
        else if (workflow.state === "debugging") status = "debugging";
        else if (workflow.state === "reviewing") status = "verifying";
        else if (workflow.state === "blocked") status = "blocked";
        else if (workflow.state === "completed") status = "approved";
      }

      // Collect all tasks assigned to this agent or role
      const memberAssignedNodes = allPlanNodes.filter(
        (n) =>
          (n.assigned_agent_id && n.assigned_agent_id === member.agent_id) ||
          (n.assigned_role && n.assigned_role.toLowerCase() === member.role.toLowerCase()),
      );

      // Determine active task
      const activeNode = memberAssignedNodes.find(
        (n) => n.status === "in_progress" || n.status === "running",
      );

      let activeTask = activeNode
        ? {
            id: activeNode.id,
            title: activeNode.title,
            status: activeNode.status,
            issueId: activeNode.materialized_issue_id || undefined,
            isCurrent: true,
          }
        : undefined;

      if (!activeTask && workflow && (workflow.state === "running" || workflow.state === "reviewing")) {
        activeTask = {
          id: workflow.id,
          title: workflow.issue_title,
          status: workflow.state,
          issueId: workflow.issue_id,
          isCurrent: true,
        };
      }

      if (!activeTask && member.current_task_title) {
        activeTask = {
          id: member.current_task_id || `task-${member.agent_id}`,
          title: member.current_task_title,
          status: member.current_task_status || (status !== "idle" ? status : "running"),
          issueId: member.current_task_id || undefined,
          isCurrent: true,
        };
      }

      // Queued tasks (pending / ready / blocked)
      const queuedNodes = memberAssignedNodes.filter(
        (n) => n.id !== activeTask?.id && ["pending", "ready", "blocked"].includes(n.status),
      );
      const queuedTasks = queuedNodes.map((n) => ({
        id: n.id,
        title: n.title,
        status: n.status,
        issueId: n.materialized_issue_id || undefined,
        isCurrent: false,
      }));

      const completedCount = memberAssignedNodes.filter((n) => n.status === "completed").length;
      const totalAssignedCount = (activeTask ? 1 : 0) + queuedTasks.length + completedCount;

      const taskTitle =
        activeTask?.title ||
        member.current_task_title ||
        workflow?.issue_title ||
        queuedTasks[0]?.title ||
        member.responsibilities?.[0] ||
        "Standby for next sprint node";

      const stationKey: OfficeZoneKey = isQaRole ? "qa_lab" : "coding_pods";
      const resolvedStatus = status === "idle" && activeTask ? "running" : status;
      const memberVisual = resolveAgentCategoryAndCharacter(member.role, false);

      const deskMeta = determineDeskToolAndSkills(
        member.role,
        resolvedStatus,
        taskTitle,
        availableSkills,
        stationKey,
        index,
      );

      desks.push({
        id: `desk-${member.agent_id}`,
        agentId: member.agent_id,
        agentName: agent?.name || member.agent_name || member.role,
        role: member.role,
        avatarUrl: agent?.avatar_url,
        status: resolvedStatus,
        currentTaskTitle: taskTitle,
        currentIssueId: activeTask?.issueId || member.current_task_id || undefined,
        activeTask,
        queuedTasks,
        completedTasksCount: completedCount,
        totalAssignedTasksCount: totalAssignedCount,
        stationKey,
        deskCoord: { x: deskX, y: deskY },
        isLeader: false,
        category: memberVisual.category,
        categoryLabel: memberVisual.categoryLabel,
        categoryColor: memberVisual.categoryColor,
        characterType: memberVisual.characterType,
        rawMember: member,
        rawAgent: agent,
        ...deskMeta,
      });
    }
  } else if (agents.length > 1) {
    // Fallback when no autonomous team snapshot exists (e.g. Campus Overview)
    const otherAgents = agents.slice(1);
    const agentCount = otherAgents.length;
    const row1Count = agentCount <= 3 ? agentCount : Math.ceil(agentCount / 2);
    const row2Count = agentCount <= 3 ? 0 : agentCount - row1Count;
    const horizontalSpacing = 340;

    otherAgents.forEach((agent, i) => {
      const isRow2 = i >= row1Count;
      const indexInRow = isRow2 ? i - row1Count : i;
      const countInCurrentRow = isRow2 ? row2Count : row1Count;

      const startX = 600 - ((countInCurrentRow - 1) * horizontalSpacing) / 2;
      const deskX = startX + indexInRow * horizontalSpacing;
      const deskY = isRow2 ? 650 : 410;

      const assignedIssues = issues.filter((issue) => issue.assignee_id === agent.id);
      const inProgressIssue = assignedIssues.find((issue) => issue.status === "in_progress");
      const todoIssues = assignedIssues.filter(
        (issue) => issue.status !== "in_progress" && issue.status !== "done",
      );
      const doneIssues = assignedIssues.filter((issue) => issue.status === "done");

      const activeTask = inProgressIssue
        ? {
            id: inProgressIssue.id,
            title: inProgressIssue.title,
            status: "in_progress",
            issueId: inProgressIssue.id,
            isCurrent: true,
          }
        : undefined;

      const queuedTasks = todoIssues.map((iss) => ({
        id: iss.id,
        title: iss.title,
        status: iss.status,
        issueId: iss.id,
        isCurrent: false,
      }));

      const visual = resolveAgentCategoryAndCharacter(agent.name, false);
      const isQa = visual.category === "qa";
      const stationKey: OfficeZoneKey = isQa ? "qa_lab" : "coding_pods";
      const resolvedStatus = activeTask ? (isQa ? "verifying" : "running") : "idle";

      const deskMeta = determineDeskToolAndSkills(
        agent.name,
        resolvedStatus,
        activeTask?.title || queuedTasks[0]?.title || "Ready for assignment",
        availableSkills,
        stationKey,
        i,
      );

      desks.push({
        id: `desk-fb-${agent.id}`,
        agentId: agent.id,
        agentName: agent.name,
        role: visual.categoryLabel,
        avatarUrl: agent.avatar_url,
        status: resolvedStatus,
        currentTaskTitle: activeTask?.title || queuedTasks[0]?.title || "Ready for assignment",
        currentIssueId: activeTask?.issueId,
        activeTask,
        queuedTasks,
        completedTasksCount: doneIssues.length,
        totalAssignedTasksCount: (activeTask ? 1 : 0) + queuedTasks.length + doneIssues.length,
        stationKey,
        deskCoord: { x: deskX, y: deskY },
        isLeader: false,
        category: visual.category,
        categoryLabel: visual.categoryLabel,
        categoryColor: visual.categoryColor,
        characterType: visual.characterType,
        rawAgent: agent,
        ...deskMeta,
      });
    });
  }

  // 3. Build Flow Edges ("Ne Nereye Gitti" Neon Highway System)
  const flowEdges: OfficeFlowEdge[] = [
    {
      id: "edge-pm-to-coding",
      fromStation: "pm_desk",
      toStation: "coding_pods",
      fromCoord: {
        x: STATION_COORDINATES.pm_desk.x,
        y: STATION_COORDINATES.pm_desk.y + 80,
      },
      toCoord: {
        x: STATION_COORDINATES.coding_pods.x,
        y: STATION_COORDINATES.coding_pods.y - 60,
      },
      color: "#06b6d4", // Cyan
      label: "Task Dispatch",
      active: runningWorkflows.length > 0 || (snapshot?.plan?.nodes?.some((n) => n.status === "in_progress") ?? false),
      pulseSpeed: "normal",
    },
    {
      id: "edge-coding-to-qa",
      fromStation: "coding_pods",
      toStation: "qa_lab",
      fromCoord: {
        x: STATION_COORDINATES.coding_pods.x + 120,
        y: STATION_COORDINATES.coding_pods.y + 40,
      },
      toCoord: {
        x: STATION_COORDINATES.qa_lab.x - 100,
        y: STATION_COORDINATES.qa_lab.y + 40,
      },
      color: "#a855f7", // Purple
      label: "Code & PR Handoff",
      active: inReviewWorkflows.length > 0 || (snapshot?.plan?.nodes?.some((n) => n.status === "verification") ?? false),
      pulseSpeed: inReviewWorkflows.length > 1 ? "fast" : "normal",
    },
    {
      id: "edge-qa-to-approval",
      fromStation: "qa_lab",
      toStation: "approval_lounge",
      fromCoord: {
        x: STATION_COORDINATES.qa_lab.x + 30,
        y: STATION_COORDINATES.qa_lab.y - 120,
      },
      toCoord: {
        x: STATION_COORDINATES.approval_lounge.x - 40,
        y: STATION_COORDINATES.approval_lounge.y + 60,
      },
      color: "#10b981", // Emerald
      label: "Verification Passed",
      active: completedNodesCount > 0 || Boolean(snapshot?.quality_gates?.some((q) => q.status === "passed")),
      pulseSpeed: "normal",
    },
    {
      id: "edge-pm-to-approval",
      fromStation: "pm_desk",
      toStation: "approval_lounge",
      fromCoord: {
        x: STATION_COORDINATES.pm_desk.x + 100,
        y: STATION_COORDINATES.pm_desk.y - 10,
      },
      toCoord: {
        x: STATION_COORDINATES.approval_lounge.x - 90,
        y: STATION_COORDINATES.approval_lounge.y - 10,
      },
      color: "#f59e0b", // Amber
      label: "Approval Required",
      active: planAwaitingApproval || pendingChangeRequests.length > 0,
      pulseSpeed: "fast",
    },
    {
      id: "edge-approval-to-pm",
      fromStation: "approval_lounge",
      toStation: "pm_desk",
      fromCoord: {
        x: STATION_COORDINATES.approval_lounge.x - 90,
        y: STATION_COORDINATES.approval_lounge.y + 25,
      },
      toCoord: {
        x: STATION_COORDINATES.pm_desk.x + 100,
        y: STATION_COORDINATES.pm_desk.y + 25,
      },
      color: "#3b82f6", // Blue
      label: "Approved Plan & Revision",
      active: (snapshot?.plan?.revision ?? 0) > 1,
      pulseSpeed: "normal",
    },
  ];

  // 4. Build Spectator Replay Events
  const recentEvents: OfficeReplayEvent[] = [];
  if (snapshot?.activity && snapshot.activity.length > 0) {
    for (const item of snapshot.activity.slice(0, 15)) {
      let fromStation: OfficeZoneKey = "pm_desk";
      let toStation: OfficeZoneKey = "coding_pods";

      if (
        item.type.includes("review") ||
        item.type.includes("qa") ||
        item.type.includes("verification")
      ) {
        fromStation = "coding_pods";
        toStation = "qa_lab";
      } else if (
        item.type.includes("approve") ||
        item.type.includes("change_request") ||
        item.type.includes("gate") ||
        item.type.includes("completed")
      ) {
        fromStation = "qa_lab";
        toStation = "approval_lounge";
      } else if (item.type.includes("plan")) {
        fromStation = "pm_desk";
        toStation = "approval_lounge";
      }

      recentEvents.push({
        id: item.id,
        title: item.title || item.type,
        timestamp: item.created_at,
        type: item.type,
        fromStation,
        toStation,
        issueId: item.issue_id,
        payloadSummary: item.detail || (item.metadata ? JSON.stringify(item.metadata).slice(0, 80) : undefined),
      });
    }
  }

  // Fallback demo events if none in snapshot
  if (recentEvents.length === 0) {
    if (planAwaitingApproval) {
      recentEvents.push({
        id: "evt-plan-await",
        title: "Roadmap Plan submitted for human sign-off",
        timestamp: new Date().toISOString(),
        type: "plan.awaiting_approval",
        fromStation: "pm_desk",
        toStation: "approval_lounge",
      });
    }
    if (runningWorkflows.length > 0) {
      recentEvents.push({
        id: "evt-workflow-run",
        title: `Task dispatched to coding pod: ${runningWorkflows[0]?.issue_title || "Feature implementation"}`,
        timestamp: new Date().toISOString(),
        type: "task.dispatched",
        fromStation: "pm_desk",
        toStation: "coding_pods",
      });
    }
  }

  const speechBubbles: InOfficeSpeechBubble[] = desks
    .map((d) => d.activeSpeechBubble)
    .filter((b): b is InOfficeSpeechBubble => Boolean(b));

  return {
    stations,
    desks,
    flowEdges,
    recentEvents,
    speechBubbles,
    chatHistory,
    stats: {
      activeAgents: desks.filter((d) => d.status !== "idle").length || desks.length,
      runningTasks: runningWorkflows.length || desks.filter((d) => d.status === "running").length,
      completedTasks: completedNodesCount,
      pendingApprovals: pendingApprovalsCount,
    },
    pendingApprovalsCount,
  };
}
