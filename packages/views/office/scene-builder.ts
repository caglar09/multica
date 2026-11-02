import type {
  Agent,
  AgentTask,
  AutonomousProjectSnapshot,
  AutonomousTeamMember,
  Issue,
  ProjectLeaderChangeRequest,
  TaskMessagePayload,
} from "@multica/core/types";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { resolveAgentCategoryAndCharacter } from "./classify";
import { deskCoordinate, stationCenter, stationForCategory, STATION_COORDINATES } from "./layout";
import {
  buildDeskSignals,
  buildOfficeBlockers,
  buildReplayEvents,
  escalationAgentId,
} from "./live-signals";
import type {
  OfficeAgentDesk,
  OfficeFlowEdge,
  OfficeStation,
  OfficeZoneKey,
  VirtualOfficeSceneData,
} from "./types";

export interface BuildVirtualOfficeParams {
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[] | null;
  agents?: Agent[];
  presenceByAgent?: ReadonlyMap<string, AgentPresenceDetail>;
  tasks?: AgentTask[];
  issues?: Issue[];
  taskMessages?: TaskMessagePayload[];
}

function makeStations(
  snapshot: AutonomousProjectSnapshot | null | undefined,
  changeRequests: ProjectLeaderChangeRequest[] | null | undefined,
): Record<OfficeZoneKey, OfficeStation> {
  const pendingApprovalsCount =
    (snapshot?.plan?.status === "awaiting_approval" ? 1 : 0) +
    (changeRequests?.filter((change) => change.state === "pending").length ?? 0) +
    (snapshot?.escalations.filter((escalation) => escalation.status === "pending").length ?? 0);
  const completedNodes = snapshot?.plan?.nodes.filter((node) => node.status === "completed").length;
  const reviews = snapshot?.workflows.filter((workflow) => workflow.state === "reviewing").length;
  const runningWorkflows = snapshot?.workflows.filter((workflow) => workflow.state === "running").length;
  const members = snapshot?.team?.members.filter((member) => member.active).length;
  const gates = snapshot?.quality_gates.length;

  const station = (
    key: OfficeZoneKey,
    titleKey: string,
    iconName: OfficeStation["iconName"],
    metrics: OfficeStation["metrics"],
    roomTheme: OfficeStation["roomTheme"],
  ): OfficeStation => ({
    key,
    titleKey,
    ...STATION_COORDINATES[key],
    iconName,
    metrics,
    ...(roomTheme ? { roomTheme } : {}),
  });

  return {
    pm_desk: station("pm_desk", "projects.office.zones.pm_desk", "FolderKanban", [
      ...(snapshot?.team ? [{ labelKey: "projects.office.metrics.leader", value: snapshot.team.leader_name }] : []),
      ...(snapshot?.plan ? [{ labelKey: "projects.office.metrics.plan_revision", value: snapshot.plan.revision }] : []),
      ...(snapshot?.plan ? [{ labelKey: "projects.office.metrics.nodes", value: snapshot.plan.nodes.length }] : []),
    ], "architecture"),
    coding_pods: station("coding_pods", "projects.office.zones.coding_pods", "Code", [
      ...(members !== undefined ? [{ labelKey: "projects.office.metrics.members", value: members }] : []),
      ...(runningWorkflows !== undefined ? [{ labelKey: "projects.office.metrics.running", value: runningWorkflows }] : []),
    ], "engineering"),
    qa_lab: station("qa_lab", "projects.office.zones.qa_lab", "CheckCircle2", [
      ...(gates !== undefined ? [{ labelKey: "projects.office.metrics.gates", value: gates }] : []),
      ...(reviews !== undefined ? [{ labelKey: "projects.office.metrics.reviews", value: reviews }] : []),
    ], "qa"),
    approval_lounge: station("approval_lounge", "projects.office.zones.approval_lounge", "ShieldAlert", [
      ...(snapshot || changeRequests !== undefined
        ? [{ labelKey: "projects.office.metrics.pending", value: pendingApprovalsCount }]
        : []),
      ...(completedNodes !== undefined ? [{ labelKey: "projects.office.metrics.completed", value: completedNodes }] : []),
    ], "executive"),
  };
}

function edge(
  id: string,
  kind: OfficeFlowEdge["kind"],
  fromStation: OfficeZoneKey,
  toStation: OfficeZoneKey,
  fromCoord: OfficeFlowEdge["fromCoord"],
  toCoord: OfficeFlowEdge["toCoord"],
  values: Pick<OfficeFlowEdge, "active"> & Partial<Pick<OfficeFlowEdge, "issueId" | "relatedCount">>,
): OfficeFlowEdge {
  return { id, kind, fromStation, toStation, fromCoord, toCoord, ...values };
}

function buildFlowEdges(
  snapshot: AutonomousProjectSnapshot | null | undefined,
  tasks: readonly AgentTask[],
  issues: readonly Issue[],
  desksByAgent: ReadonlyMap<string, OfficeAgentDesk>,
): OfficeFlowEdge[] {
  const edges: OfficeFlowEdge[] = [];
  const issueById = new Map(issues.map((issue) => [issue.id, issue]));
  const workflows = snapshot?.workflows ?? [];
  const nodes = snapshot?.plan?.nodes ?? [];
  const nodesByIdOrKey = new Map(nodes.flatMap((node) => [[node.id, node] as const, [node.key, node] as const]));

  if (snapshot?.team) {
    for (const task of tasks) {
      if (task.status !== "running" || !task.issue_id) continue;
      const desk = desksByAgent.get(task.agent_id);
      if (!desk) continue;
      edges.push(edge(
        `task-dispatch:${task.id}`,
        "task_dispatch",
        "pm_desk",
        desk.stationKey,
        stationCenter("pm_desk"),
        desk.deskCoord,
        { issueId: task.issue_id, relatedCount: 1, active: true },
      ));
    }
  }

  for (const workflow of workflows) {
    if (workflow.state !== "reviewing" || !workflow.owner_agent_id || !workflow.reviewer_agent_id) continue;
    const owner = desksByAgent.get(workflow.owner_agent_id);
    const reviewer = desksByAgent.get(workflow.reviewer_agent_id);
    if (!owner || !reviewer) continue;
    edges.push(edge(
      `review-handoff:${workflow.id}`,
      "review_handoff",
      owner.stationKey,
      reviewer.stationKey,
      owner.deskCoord,
      reviewer.deskCoord,
      { issueId: workflow.issue_id, relatedCount: workflow.review_cycles, active: true },
    ));
  }

  for (const dependency of snapshot?.plan?.edges ?? []) {
    const fromNode = nodesByIdOrKey.get(dependency.from);
    const toNode = nodesByIdOrKey.get(dependency.to);
    if (!fromNode?.assigned_agent_id || !toNode?.assigned_agent_id) continue;
    const from = desksByAgent.get(fromNode.assigned_agent_id);
    const to = desksByAgent.get(toNode.assigned_agent_id);
    if (!from || !to) continue;
    edges.push(edge(
      `plan-dependency:${fromNode.id}:${toNode.id}:${dependency.type}`,
      "plan_dependency",
      from.stationKey,
      to.stationKey,
      from.deskCoord,
      to.deskCoord,
      {
        ...(toNode.materialized_issue_id ? { issueId: toNode.materialized_issue_id } : {}),
        relatedCount: 1,
        active: fromNode.status !== "completed" && toNode.status !== "completed",
      },
    ));
  }

  for (const issue of issues) {
    for (const dependency of issue.blocked_by ?? []) {
      if (dependency.related_issue.done) continue;
      const dependentOwner = issue.assignee_type === "agent" && issue.assignee_id
        ? desksByAgent.get(issue.assignee_id)
        : undefined;
      const blockerIssue = issueById.get(dependency.depends_on_issue_id);
      const blockerOwner = blockerIssue?.assignee_type === "agent" && blockerIssue.assignee_id
        ? desksByAgent.get(blockerIssue.assignee_id)
        : undefined;
      if (!dependentOwner || !blockerOwner) continue;
      edges.push(edge(
        `issue-dependency:${issue.id}:${dependency.depends_on_issue_id}`,
        "issue_dependency",
        blockerOwner.stationKey,
        dependentOwner.stationKey,
        blockerOwner.deskCoord,
        dependentOwner.deskCoord,
        { issueId: issue.id, relatedCount: 1, active: true },
      ));
    }
  }

  for (const escalation of snapshot?.escalations ?? []) {
    if (escalation.status !== "pending") continue;
    const agentId = escalationAgentId(escalation.context);
    const desk = agentId ? desksByAgent.get(agentId) : undefined;
    if (!desk) continue;
    const issueId = typeof escalation.context.issue_id === "string" ? escalation.context.issue_id : undefined;
    edges.push(edge(
      `escalation:${escalation.id}`,
      "escalation",
      desk.stationKey,
      "approval_lounge",
      desk.deskCoord,
      stationCenter("approval_lounge"),
      { ...(issueId ? { issueId } : {}), relatedCount: 1, active: true },
    ));
  }

  return edges;
}

export function buildVirtualOfficeScene({
  snapshot,
  changeRequests,
  agents = [],
  presenceByAgent = new Map(),
  tasks = [],
  issues = [],
  taskMessages = [],
}: BuildVirtualOfficeParams): VirtualOfficeSceneData {
  const teamMembers = new Map<string, AutonomousTeamMember>();
  for (const member of snapshot?.team?.members ?? []) {
    if (member.active) teamMembers.set(member.agent_id, member);
  }
  const leaderId = snapshot?.team?.leader_agent_id || undefined;
  const taskMessagesByRun = new Map<string, TaskMessagePayload[]>();
  for (const message of taskMessages) {
    const list = taskMessagesByRun.get(message.task_id);
    if (list) list.push(message);
    else taskMessagesByRun.set(message.task_id, [message]);
  }

  const stationCounts = new Map<OfficeZoneKey, number>();
  const desks: OfficeAgentDesk[] = agents
    .filter((agent) => !agent.archived_at)
    .map((agent) => {
      const member = teamMembers.get(agent.id);
      const isLeader = agent.id === leaderId;
      const { category, characterType } = resolveAgentCategoryAndCharacter(
        member?.family ?? "",
        member?.role ?? agent.description ?? "",
        isLeader,
      );
      const stationKey = stationForCategory(category);
      const index = stationCounts.get(stationKey) ?? 0;
      stationCounts.set(stationKey, index + 1);
      const desk = buildDeskSignals({
        agent,
        member,
        isLeader,
        tasks,
        issues,
        snapshot,
        presence: presenceByAgent.get(agent.id),
        taskMessagesByRun,
      });
      return {
        ...desk,
        category,
        characterType,
        stationKey,
        deskCoord: deskCoordinate(stationKey, index),
      };
    });

  const desksByAgent = new Map(
    desks.flatMap((desk) => desk.agentId ? [[desk.agentId, desk] as const] : []),
  );
  const stations = makeStations(snapshot, changeRequests);
  const blockers = buildOfficeBlockers({ snapshot, changeRequests, agents, tasks, issues, presenceByAgent });
  const flowEdges = buildFlowEdges(snapshot, tasks, issues, desksByAgent);
  const recentEvents = buildReplayEvents({ snapshot, desksByAgent, leaderId });
  const runningTasks = tasks.filter((task) => task.status === "running");
  const pendingApprovalsCount =
    (snapshot?.plan?.status === "awaiting_approval" ? 1 : 0) +
    (changeRequests?.filter((change) => change.state === "pending").length ?? 0) +
    (snapshot?.escalations.filter((escalation) => escalation.status === "pending").length ?? 0);
  const completedTasks = snapshot?.plan
    ? snapshot.plan.nodes.filter((node) => node.status === "completed").length
    : snapshot?.workflows.filter((workflow) => workflow.state === "completed").length ?? 0;

  return {
    stations,
    desks,
    flowEdges,
    blockers,
    recentEvents,
    speechBubbles: desks.flatMap((desk) => desk.activeSpeechBubble ? [desk.activeSpeechBubble] : []),
    stats: {
      activeAgents: new Set(runningTasks.map((task) => task.agent_id)).size,
      runningTasks: runningTasks.length,
      completedTasks,
      pendingApprovals: pendingApprovalsCount,
      blocked: blockers.filter((item) => ["issue_dependency", "plan_dependency", "blocked_agent", "runtime_unavailable"].includes(item.kind)).length,
      failed: blockers.filter((item) => item.kind === "failed_run" || item.kind === "failed_gate").length,
      waiting: blockers.filter((item) => item.severity === "waiting").length,
      resumable: snapshot?.health.resumable ?? 0,
    },
    pendingApprovalsCount,
  };
}
