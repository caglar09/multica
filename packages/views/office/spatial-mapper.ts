import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  Agent,
  Issue,
} from "@multica/core/types";
import type {
  OfficeZoneKey,
  OfficeStation,
  OfficeAgentDesk,
  OfficeFlowEdge,
  OfficeReplayEvent,
  VirtualOfficeSceneData,
  AgentDeskStatus,
} from "./types";

export interface BuildVirtualOfficeParams {
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[] | null;
  agents?: Agent[];
  issues?: Issue[];
}

export const STATION_COORDINATES: Record<
  OfficeZoneKey,
  { x: number; y: number; width: number; height: number }
> = {
  pm_desk: { x: 250, y: 150, width: 280, height: 160 },
  coding_pods: { x: 350, y: 490, width: 560, height: 320 },
  qa_lab: { x: 950, y: 490, width: 420, height: 320 },
  approval_lounge: { x: 920, y: 150, width: 280, height: 160 },
};

export function buildVirtualOfficeScene({
  snapshot,
  changeRequests = [],
  agents = [],
  issues = [],
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

  // 1. Build 4 Stations
  const stations: Record<OfficeZoneKey, OfficeStation> = {
    pm_desk: {
      key: "pm_desk",
      titleKey: "office.zones.pm_desk",
      defaultTitle: "Planning & PM Desk",
      description: "Strategy, Roadmap Breakdown & Autonomous Coordination",
      x: STATION_COORDINATES.pm_desk.x,
      y: STATION_COORDINATES.pm_desk.y,
      width: STATION_COORDINATES.pm_desk.width,
      height: STATION_COORDINATES.pm_desk.height,
      statusColor: "#0ea5e9", // Sky Blue
      iconName: "FolderKanban",
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
      defaultTitle: "Active Coding Pods",
      description: "Parallel Agent Workbenches & Feature Implementation",
      x: STATION_COORDINATES.coding_pods.x,
      y: STATION_COORDINATES.coding_pods.y,
      width: STATION_COORDINATES.coding_pods.width,
      height: STATION_COORDINATES.coding_pods.height,
      statusColor: "#6366f1", // Indigo
      iconName: "Code",
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
      description: "Quality Gates, Test Runner & Peer Code Review",
      x: STATION_COORDINATES.qa_lab.x,
      y: STATION_COORDINATES.qa_lab.y,
      width: STATION_COORDINATES.qa_lab.width,
      height: STATION_COORDINATES.qa_lab.height,
      statusColor: "#a855f7", // Purple
      iconName: "CheckCircle2",
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
      defaultTitle: "Decision & Approval Lounge",
      description: "Human-in-the-Loop Gate, Plan Approvals & Completed Goals",
      x: STATION_COORDINATES.approval_lounge.x,
      y: STATION_COORDINATES.approval_lounge.y,
      width: STATION_COORDINATES.approval_lounge.width,
      height: STATION_COORDINATES.approval_lounge.height,
      statusColor: pendingApprovalsCount > 0 ? "#f59e0b" : "#10b981", // Amber or Emerald
      iconName: "ShieldAlert",
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
        y: STATION_COORDINATES.pm_desk.y + 15,
      },
      rawAgent: leaderAgent,
    });
  }

  // Team Members
  const members = snapshot?.team?.members ?? [];
  if (members.length > 0) {
    let devIndex = 0;
    let qaIndex = 0;

    for (const member of members) {
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

      if (isQaRole) {
        // QA Station Desk - 2 columns grid with 200px column gap and 130px row gap
        const col = qaIndex % 2;
        const row = Math.floor(qaIndex / 2);
        const deskX = STATION_COORDINATES.qa_lab.x - 90 + col * 200;
        const deskY = STATION_COORDINATES.qa_lab.y - 40 + row * 135;

        desks.push({
          id: `desk-qa-${member.agent_id}`,
          agentId: member.agent_id,
          agentName: agent?.name || member.agent_name || member.role,
          role: member.role,
          avatarUrl: agent?.avatar_url,
          status: status === "idle" && inReviewWorkflows.length > 0 ? "verifying" : status,
          currentTaskTitle: taskTitle,
          currentIssueId: activeTask?.issueId || member.current_task_id || undefined,
          activeTask,
          queuedTasks,
          completedTasksCount: completedCount,
          totalAssignedTasksCount: totalAssignedCount,
          stationKey: "qa_lab",
          deskCoord: { x: deskX, y: deskY },
          rawMember: member,
          rawAgent: agent,
        });
        qaIndex++;
      } else {
        // Coding Pods Station Desk - 2 columns grid with 240px column gap and 135px row gap (generous spacing!)
        const col = devIndex % 2;
        const row = Math.floor(devIndex / 2);
        const deskX = STATION_COORDINATES.coding_pods.x - 120 + col * 240;
        const deskY = STATION_COORDINATES.coding_pods.y - 45 + row * 135;

        desks.push({
          id: `desk-dev-${member.agent_id}`,
          agentId: member.agent_id,
          agentName: agent?.name || member.agent_name || member.role,
          role: member.role,
          avatarUrl: agent?.avatar_url,
          status: status === "idle" && activeTask ? "running" : status,
          currentTaskTitle: taskTitle,
          currentIssueId: activeTask?.issueId || member.current_task_id || undefined,
          activeTask,
          queuedTasks,
          completedTasksCount: completedCount,
          totalAssignedTasksCount: totalAssignedCount,
          stationKey: "coding_pods",
          deskCoord: { x: deskX, y: deskY },
          rawMember: member,
          rawAgent: agent,
        });
        devIndex++;
      }
    }
  } else if (agents.length > 1) {
    // Fallback when no autonomous team snapshot exists (e.g. Campus Overview)
    const otherAgents = agents.slice(1);
    otherAgents.forEach((agent, i) => {
      const isQa = agent.name.toLowerCase().includes("qa") || agent.name.toLowerCase().includes("review");
      const assignedIssues = issues.filter((issue) => issue.assignee_id === agent.id);
      const inProgressIssue = assignedIssues.find((issue) => issue.status === "in_progress");
      const todoIssues = assignedIssues.filter((issue) => issue.status !== "in_progress" && issue.status !== "done");
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

      if (isQa) {
        const col = i % 2;
        const row = Math.floor(i / 2);
        desks.push({
          id: `desk-qa-fb-${agent.id}`,
          agentId: agent.id,
          agentName: agent.name,
          role: "Quality & Review",
          avatarUrl: agent.avatar_url,
          status: activeTask ? "verifying" : "idle",
          currentTaskTitle: activeTask?.title || queuedTasks[0]?.title || "Standby verification",
          currentIssueId: activeTask?.issueId,
          activeTask,
          queuedTasks,
          completedTasksCount: doneIssues.length,
          totalAssignedTasksCount: (activeTask ? 1 : 0) + queuedTasks.length + doneIssues.length,
          stationKey: "qa_lab",
          deskCoord: {
            x: STATION_COORDINATES.qa_lab.x - 90 + col * 200,
            y: STATION_COORDINATES.qa_lab.y - 40 + row * 135,
          },
          rawAgent: agent,
        });
      } else {
        const col = i % 2;
        const row = Math.floor(i / 2);
        desks.push({
          id: `desk-dev-fb-${agent.id}`,
          agentId: agent.id,
          agentName: agent.name,
          role: "Engineer",
          avatarUrl: agent.avatar_url,
          status: activeTask ? "running" : "idle",
          currentTaskTitle: activeTask?.title || queuedTasks[0]?.title || "Ready for assignment",
          currentIssueId: activeTask?.issueId,
          activeTask,
          queuedTasks,
          completedTasksCount: doneIssues.length,
          totalAssignedTasksCount: (activeTask ? 1 : 0) + queuedTasks.length + doneIssues.length,
          stationKey: "coding_pods",
          deskCoord: {
            x: STATION_COORDINATES.coding_pods.x - 120 + col * 240,
            y: STATION_COORDINATES.coding_pods.y - 45 + row * 135,
          },
          rawAgent: agent,
        });
      }
    });
  }

  // 3. Build Flow Edges ("Ne Nereye Gitti" Neon Highway System)
  const flowEdges: OfficeFlowEdge[] = [
    {
      id: "edge-pm-to-coding",
      fromStation: "pm_desk",
      toStation: "coding_pods",
      fromCoord: {
        x: STATION_COORDINATES.pm_desk.x + 40,
        y: STATION_COORDINATES.pm_desk.y + 70,
      },
      toCoord: {
        x: STATION_COORDINATES.coding_pods.x - 30,
        y: STATION_COORDINATES.coding_pods.y - 100,
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
        x: STATION_COORDINATES.coding_pods.x + 220,
        y: STATION_COORDINATES.coding_pods.y + 10,
      },
      toCoord: {
        x: STATION_COORDINATES.qa_lab.x - 180,
        y: STATION_COORDINATES.qa_lab.y + 10,
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
        y: STATION_COORDINATES.qa_lab.y - 90,
      },
      toCoord: {
        x: STATION_COORDINATES.approval_lounge.x - 20,
        y: STATION_COORDINATES.approval_lounge.y + 70,
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
        x: STATION_COORDINATES.pm_desk.x + 130,
        y: STATION_COORDINATES.pm_desk.y - 20,
      },
      toCoord: {
        x: STATION_COORDINATES.approval_lounge.x - 130,
        y: STATION_COORDINATES.approval_lounge.y - 20,
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
        x: STATION_COORDINATES.approval_lounge.x - 130,
        y: STATION_COORDINATES.approval_lounge.y + 20,
      },
      toCoord: {
        x: STATION_COORDINATES.pm_desk.x + 130,
        y: STATION_COORDINATES.pm_desk.y + 20,
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

  return {
    stations,
    desks,
    flowEdges,
    recentEvents,
    stats: {
      activeAgents: desks.filter((d) => d.status !== "idle").length || desks.length,
      runningTasks: runningWorkflows.length || desks.filter((d) => d.status === "running").length,
      completedTasks: completedNodesCount,
      pendingApprovals: pendingApprovalsCount,
    },
    pendingApprovalsCount,
  };
}
