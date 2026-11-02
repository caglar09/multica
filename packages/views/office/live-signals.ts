import type {
  Agent,
  AgentTask,
  AutonomousProjectSnapshot,
  Issue,
  AutonomousTeamMember,
  ProjectLeaderChangeRequest,
  TaskMessagePayload,
} from "@multica/core/types";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type {
  ActiveToolCall,
  AgentDeskStatus,
  AgentTaskItem,
  InOfficeSpeechBubble,
  OfficeAgentDesk,
  OfficeBlocker,
  OfficeBlockerKind,
  OfficeReplayEvent,
  OfficeZoneKey,
} from "./types";

const QUEUED_TASK_STATUSES = new Set(["queued", "dispatched", "waiting_local_directory", "deferred"]);
const BLOCKER_REASON_KEYS: Record<OfficeBlockerKind, string> = {
  issue_dependency: "projects.office.blockers.reasons.issue_dependency",
  plan_dependency: "projects.office.blockers.reasons.plan_dependency",
  failed_run: "projects.office.blockers.reasons.failed_run",
  failed_gate: "projects.office.blockers.reasons.failed_gate",
  blocked_agent: "projects.office.blockers.reasons.blocked_agent",
  runtime_unavailable: "projects.office.blockers.reasons.runtime_unavailable",
  approval_required: "projects.office.blockers.reasons.approval_required",
};

export interface BuildDeskSignalsInput {
  agent: Agent;
  member?: AutonomousTeamMember;
  isLeader: boolean;
  tasks: readonly AgentTask[];
  issues: readonly Issue[];
  snapshot?: AutonomousProjectSnapshot | null;
  presence?: AgentPresenceDetail;
  taskMessagesByRun: ReadonlyMap<string, readonly TaskMessagePayload[]>;
}

export type DeskLiveSignals = Omit<OfficeAgentDesk, "stationKey" | "deskCoord" | "category" | "categoryColor" | "characterType">;

function timestamp(value: string | null | undefined): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function issueTitle(issueId: string, issuesById: ReadonlyMap<string, Issue>): string | undefined {
  return issuesById.get(issueId)?.title;
}

function taskTitle(
  task: AgentTask,
  input: BuildDeskSignalsInput,
  issuesById: ReadonlyMap<string, Issue>,
): string {
  if (task.issue_id) {
    const fromIssue = issueTitle(task.issue_id, issuesById);
    if (fromIssue) return fromIssue;
    const fromWorkflow = input.snapshot?.workflows.find((workflow) => workflow.issue_id === task.issue_id)?.issue_title;
    if (fromWorkflow) return fromWorkflow;
    const fromNode = input.snapshot?.plan?.nodes.find((node) => node.materialized_issue_id === task.issue_id)?.title;
    if (fromNode) return fromNode;
  }
  if (input.member?.current_task_id === task.id && input.member.current_task_title) {
    return input.member.current_task_title;
  }
  return task.trigger_summary ?? "";
}

function toTaskItem(task: AgentTask, title: string, isCurrent = false): AgentTaskItem {
  return {
    id: task.id,
    title,
    status: task.status,
    ...(task.issue_id ? { issueId: task.issue_id } : {}),
    ...(isCurrent ? { isCurrent: true } : {}),
  };
}

function getTaskMessages(runId: string | undefined, map: ReadonlyMap<string, readonly TaskMessagePayload[]>) {
  return runId ? [...(map.get(runId) ?? [])].sort((a, b) => a.seq - b.seq) : [];
}

function toolSignals(messages: readonly TaskMessagePayload[], skills: Agent["skills"]): {
  activeTool?: ActiveToolCall;
  recentTools?: ActiveToolCall[];
  activeSkill?: { name: string };
} {
  const calls = new Map<string, ActiveToolCall>();
  let activeSkill: { name: string } | undefined;

  for (const message of messages) {
    if (message.type === "tool_use" && message.call_id && message.tool) {
      const startedAt = timestamp(message.created_at);
      calls.set(message.call_id, {
        name: message.tool,
        status: "running",
        ...(startedAt > 0 ? { startedAt } : {}),
      });

      if (message.tool.toLowerCase() === "skill") {
        const inputSkill = [message.input?.skill, message.input?.skill_name, message.input?.name]
          .find((value): value is string => typeof value === "string");
        const skill = inputSkill && skills.find((item) => item.id === inputSkill || item.name === inputSkill);
        if (skill) activeSkill = { name: skill.name };
      }
    } else if (message.type === "tool_result" && message.call_id) {
      const call = calls.get(message.call_id);
      if (call) calls.set(message.call_id, { ...call, status: "completed" });
    }
  }

  const recentTools = [...calls.values()].slice(-5);
  const activeTool = [...calls.values()].reverse().find((call) => call.status === "running");
  return {
    ...(activeTool ? { activeTool } : {}),
    ...(recentTools.length > 0 ? { recentTools } : {}),
    ...(activeSkill ? { activeSkill } : {}),
  };
}

function speechBubble(
  messages: readonly TaskMessagePayload[],
  deskId: string,
  agentName: string,
): InOfficeSpeechBubble | undefined {
  const message = [...messages]
    .reverse()
    .find((item) => (item.type === "text" || item.type === "thinking") && item.content?.trim() && timestamp(item.created_at) > 0);
  if (!message?.content || !message.created_at) return undefined;
  const createdAt = timestamp(message.created_at);
  return {
    id: `${message.task_id}:${message.seq}`,
    deskId,
    agentName,
    text: message.content,
    createdAt,
    expiresAt: createdAt + 60_000,
    kind: message.type === "thinking" ? "thought" : "announcement",
  };
}

function taskDeskStatus(
  agent: Agent,
  activeTask: AgentTask | undefined,
  latestTask: AgentTask | undefined,
  issue: Issue | undefined,
  isReviewing: boolean,
): AgentDeskStatus {
  if (issue?.is_blocked || agent.status === "blocked") return "blocked";
  if (activeTask && isReviewing) return "verifying";
  if (activeTask) return "running";
  if (agent.status === "error" || latestTask?.status === "failed") return "failed";
  if (agent.status === "working") return "running";
  return "idle";
}

export function buildDeskSignals(input: BuildDeskSignalsInput): DeskLiveSignals {
  const agentTasks = input.tasks.filter((task) => task.agent_id === input.agent.id);
  const runningTasks = agentTasks
    .filter((task) => task.status === "running")
    .sort((a, b) => timestamp(b.started_at ?? b.created_at) - timestamp(a.started_at ?? a.created_at));
  const queuedTasks = agentTasks
    .filter((task) => QUEUED_TASK_STATUSES.has(task.status))
    .sort((a, b) => timestamp(a.created_at) - timestamp(b.created_at));
  const activeRun = runningTasks[0];
  const issuesById = new Map(input.issues.map((issue) => [issue.id, issue]));
  const activeIssue = activeRun?.issue_id ? issuesById.get(activeRun.issue_id) : undefined;
  const reviewing = Boolean(activeRun && input.snapshot?.workflows.some(
    (workflow) => workflow.issue_id === activeRun.issue_id && workflow.state === "reviewing",
  ));
  const messages = getTaskMessages(activeRun?.id, input.taskMessagesByRun);
  const deskId = `desk-${input.agent.id}`;
  const name = input.agent.name;
  const activeTaskTitle = activeRun ? taskTitle(activeRun, input, issuesById) : undefined;
  const latestTask = [...agentTasks].sort((a, b) => timestamp(b.completed_at ?? b.created_at) - timestamp(a.completed_at ?? a.created_at))[0];
  const assignedIssueIds = new Set<string>();
  for (const issue of input.issues) {
    if (issue.assignee_type === "agent" && issue.assignee_id === input.agent.id) assignedIssueIds.add(issue.id);
  }
  for (const node of input.snapshot?.plan?.nodes ?? []) {
    if (node.assigned_agent_id === input.agent.id && node.materialized_issue_id) assignedIssueIds.add(node.materialized_issue_id);
  }
  const completedPlanNodes = (input.snapshot?.plan?.nodes ?? []).filter(
    (node) => node.assigned_agent_id === input.agent.id && node.status === "completed",
  ).length;
  const unmaterializedAssignedNodes = (input.snapshot?.plan?.nodes ?? []).filter(
    (node) => node.assigned_agent_id === input.agent.id && !node.materialized_issue_id,
  ).length;
  const toolState = toolSignals(messages, input.agent.skills ?? []);
  const bubble = activeRun ? speechBubble(messages, deskId, name) : undefined;
  const teamRole = input.member?.role ?? input.agent.description ?? "";

  return {
    id: deskId,
    agentId: input.agent.id,
    agentName: name,
    role: teamRole,
    avatarUrl: input.agent.avatar_url,
    status: taskDeskStatus(input.agent, activeRun, latestTask, activeIssue, reviewing),
    ...(activeTaskTitle ? { currentTaskTitle: activeTaskTitle } : {}),
    ...(activeRun?.issue_id ? { currentIssueId: activeRun.issue_id } : {}),
    ...(activeRun ? { activeTask: toTaskItem(activeRun, activeTaskTitle ?? "", true) } : {}),
    queuedTasks: queuedTasks.map((task) => toTaskItem(task, taskTitle(task, input, issuesById))),
    completedTasksCount: completedPlanNodes,
    totalAssignedTasksCount: assignedIssueIds.size + unmaterializedAssignedNodes,
    ...(input.member ? { rawMember: input.member } : {}),
    rawAgent: input.agent,
    ...(input.isLeader ? { isLeader: true } : {}),
    ...(input.presence ? { isDaemonActive: input.presence.availability === "online" } : {}),
    allowedSkills: (input.agent.skills ?? []).filter((skill) => skill.enabled !== false).map((skill) => skill.name),
    ...toolState,
    ...(bubble ? { activeSpeechBubble: bubble } : {}),
  };
}

function blocker(kind: OfficeBlockerKind, values: Omit<OfficeBlocker, "kind" | "reasonKey">): OfficeBlocker {
  return { ...values, kind, reasonKey: BLOCKER_REASON_KEYS[kind] };
}

function unresolvedIssueDependencies(issue: Issue): NonNullable<Issue["blocked_by"]> {
  return (issue.blocked_by ?? []).filter((dependency) => !dependency.related_issue.done);
}

export function buildOfficeBlockers(input: {
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[] | null;
  agents: readonly Agent[];
  tasks: readonly AgentTask[];
  issues: readonly Issue[];
  presenceByAgent: ReadonlyMap<string, AgentPresenceDetail>;
}): OfficeBlocker[] {
  const result: OfficeBlocker[] = [];
  const issuesById = new Map(input.issues.map((issue) => [issue.id, issue]));
  const workflowsByIssue = new Map((input.snapshot?.workflows ?? []).map((workflow) => [workflow.issue_id, workflow]));

  for (const issue of input.issues) {
    const dependencies = unresolvedIssueDependencies(issue);
    if (!issue.is_blocked && dependencies.length === 0) continue;
    const dependencyCount = issue.unresolved_blocker_count ?? dependencies.length;
    result.push(blocker("issue_dependency", {
      id: `issue-dependency:${issue.id}`,
      severity: "waiting",
      issueId: issue.id,
      ...(dependencies.length ? { dependencyIssueIds: dependencies.map((item) => item.depends_on_issue_id) } : {}),
      ...(dependencyCount > 0 ? { params: { dependencyCount } } : {}),
      action: { kind: "open_issue", targetId: issue.id },
    }));
  }

  const planNodes = input.snapshot?.plan?.nodes ?? [];
  const nodesByKey = new Map(planNodes.flatMap((node) => [[node.key, node] as const, [node.id, node] as const]));
  for (const node of planNodes) {
    if (node.status !== "blocked" && !node.blocked_category) continue;
    const dependencyCount = (input.snapshot?.plan?.edges ?? []).filter((edge) => {
      const target = nodesByKey.get(edge.to);
      const source = nodesByKey.get(edge.from);
      return target?.id === node.id && source?.status !== "completed";
    }).length;
    const issueId = node.materialized_issue_id ?? undefined;
    result.push(blocker("plan_dependency", {
      id: `plan-dependency:${node.id}`,
      severity: "waiting",
      ...(node.assigned_agent_id ? { agentId: node.assigned_agent_id } : {}),
      ...(issueId ? { issueId } : {}),
      ...(dependencyCount > 0 ? { params: { dependencyCount } } : {}),
      action: issueId && issuesById.has(issueId)
        ? { kind: "open_issue", targetId: issueId }
        : { kind: "none" },
    }));
  }

  for (const gate of input.snapshot?.quality_gates ?? []) {
    if (!gate.required || !["failed", "error"].includes(gate.status)) continue;
    const node = planNodes.find((item) => item.id === gate.node_id);
    result.push(blocker("failed_gate", {
      id: `failed-gate:${gate.id}`,
      severity: "error",
      ...(node?.assigned_agent_id ? { agentId: node.assigned_agent_id } : {}),
      ...(node?.materialized_issue_id ? { issueId: node.materialized_issue_id } : {}),
      action: { kind: "none" },
    }));
  }

  for (const task of input.tasks) {
    if (task.status !== "failed") continue;
    const issueId = task.issue_id || undefined;
    result.push(blocker("failed_run", {
      id: `failed-run:${task.id}`,
      severity: "error",
      agentId: task.agent_id,
      ...(issueId ? { issueId } : {}),
      runId: task.id,
      action: issueId
        ? { kind: "retry_run", targetId: task.id }
        : { kind: "none" },
    }));
  }

  for (const action of input.snapshot?.actions ?? []) {
    if (!["failed", "error"].includes(action.status)) continue;
    const issueId = [...workflowsByIssue.values()].find((workflow) => workflow.id === action.run_id)?.issue_id;
    result.push(blocker("failed_run", {
      id: `failed-action:${action.id}`,
      severity: "error",
      ...(issueId ? { issueId } : {}),
      runId: action.run_id,
      action: { kind: "retry_action", targetId: action.id },
    }));
  }

  for (const diagnostic of input.snapshot?.diagnostics ?? []) {
    if (!diagnostic.can_resume || !diagnostic.resume_action) continue;
    if (diagnostic.resume_action === "resolve_escalation" && diagnostic.action_id) {
      result.push(blocker("approval_required", {
        id: `diagnostic:${diagnostic.code}:${diagnostic.action_id}`,
        severity: "waiting",
        ...(diagnostic.issue_id ? { issueId: diagnostic.issue_id } : {}),
        ...(diagnostic.task_id ? { runId: diagnostic.task_id } : {}),
        action: { kind: "resolve_escalation", targetId: diagnostic.action_id },
      }));
    } else if (diagnostic.resume_action === "retry_action" && diagnostic.action_id) {
      result.push(blocker("failed_run", {
        id: `diagnostic:${diagnostic.code}:${diagnostic.action_id}`,
        severity: "error",
        ...(diagnostic.issue_id ? { issueId: diagnostic.issue_id } : {}),
        ...(diagnostic.task_id ? { runId: diagnostic.task_id } : {}),
        action: { kind: "retry_action", targetId: diagnostic.action_id },
      }));
    } else if (diagnostic.resume_action === "rerun_issue" && diagnostic.task_id && diagnostic.issue_id) {
      result.push(blocker("failed_run", {
        id: `diagnostic:${diagnostic.code}:${diagnostic.task_id}`,
        severity: "error",
        issueId: diagnostic.issue_id,
        runId: diagnostic.task_id,
        action: { kind: "retry_run", targetId: diagnostic.task_id },
      }));
    }
  }

  for (const escalation of input.snapshot?.escalations ?? []) {
    if (escalation.status !== "pending") continue;
    result.push(blocker("approval_required", {
      id: `escalation:${escalation.id}`,
      severity: "waiting",
      action: { kind: "resolve_escalation", targetId: escalation.id },
    }));
  }

  if (input.snapshot?.plan?.status === "awaiting_approval" && input.snapshot.plan.id) {
    result.push(blocker("approval_required", {
      id: `plan-approval:${input.snapshot.plan.id}`,
      severity: "waiting",
      action: { kind: "approve_plan", targetId: input.snapshot.plan.id },
    }));
  }

  for (const change of input.changeRequests ?? []) {
    if (change.state !== "pending") continue;
    result.push(blocker("approval_required", {
      id: `change-approval:${change.id}`,
      severity: "waiting",
      action: { kind: "none" },
    }));
  }

  for (const agent of input.agents) {
    const presence = input.presenceByAgent.get(agent.id);
    const queued = input.tasks.some((task) => task.agent_id === agent.id && QUEUED_TASK_STATUSES.has(task.status));
    if (agent.status === "blocked" || agent.status === "error") {
      result.push(blocker("blocked_agent", {
        id: `blocked-agent:${agent.id}`,
        severity: agent.status === "error" ? "error" : "warning",
        agentId: agent.id,
        action: { kind: "none" },
      }));
    }
    if (queued && presence && (presence.availability === "offline" || presence.availability === "unstable")) {
      result.push(blocker("runtime_unavailable", {
        id: `runtime-unavailable:${agent.id}`,
        severity: "waiting",
        agentId: agent.id,
        action: { kind: "none" },
      }));
    }
  }

  const seen = new Set<string>();
  return result.filter((item) => {
    const key = item.action.kind === "retry_action"
      ? `${item.kind}:action:${item.action.targetId}`
      : item.action.kind === "retry_run" && item.runId
        ? `${item.kind}:run:${item.runId}`
        : item.id;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function contentString(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

export function escalationAgentId(context: Record<string, unknown>): string | undefined {
  return contentString(context.agent_id) ?? contentString(context.agentId);
}

export function buildReplayEvents(input: {
  snapshot?: AutonomousProjectSnapshot | null;
  desksByAgent: ReadonlyMap<string, OfficeAgentDesk>;
  leaderId?: string;
}): OfficeReplayEvent[] {
  const snapshot = input.snapshot;
  if (!snapshot) return [];
  const workflowsByIssue = new Map(snapshot.workflows.map((workflow) => [workflow.issue_id, workflow]));
  const events: OfficeReplayEvent[] = [];

  for (const activity of snapshot.activity) {
    const agentDesk = activity.agent_id ? input.desksByAgent.get(activity.agent_id) : undefined;
    const leaderDesk = input.leaderId ? input.desksByAgent.get(input.leaderId) : undefined;
    let fromStation: OfficeZoneKey | undefined;
    let toStation: OfficeZoneKey | undefined;
    let agentId = activity.agent_id;
    const workflow = activity.issue_id ? workflowsByIssue.get(activity.issue_id) : undefined;

    switch (activity.type) {
      case "task.dispatched":
        if (agentDesk) {
          fromStation = "pm_desk";
          toStation = agentDesk.stationKey;
        }
        break;
      case "task.completed":
      case "task.failed":
        if (agentDesk && leaderDesk) {
          fromStation = agentDesk.stationKey;
          toStation = leaderDesk.stationKey;
        }
        break;
      case "workflow.review_requested":
        if (workflow?.owner_agent_id && workflow.reviewer_agent_id) {
          const owner = input.desksByAgent.get(workflow.owner_agent_id);
          const reviewer = input.desksByAgent.get(workflow.reviewer_agent_id);
          if (owner && reviewer) {
            fromStation = owner.stationKey;
            toStation = reviewer.stationKey;
            agentId = activity.agent_id ?? workflow.owner_agent_id;
          }
        }
        break;
      case "workflow.reviewed":
        if (workflow?.owner_agent_id && workflow.reviewer_agent_id) {
          const owner = input.desksByAgent.get(workflow.owner_agent_id);
          const reviewer = input.desksByAgent.get(workflow.reviewer_agent_id);
          if (owner && reviewer) {
            fromStation = reviewer.stationKey;
            toStation = owner.stationKey;
            agentId = activity.agent_id ?? workflow.reviewer_agent_id;
          }
        }
        break;
      case "plan.approved":
        if (agentDesk) {
          fromStation = "approval_lounge";
          toStation = agentDesk.stationKey;
        }
        break;
      case "escalation.created":
        if (agentDesk) {
          fromStation = agentDesk.stationKey;
          toStation = "approval_lounge";
        }
        break;
      default:
        continue;
    }

    if (!fromStation || !toStation) continue;
    events.push({
      id: activity.id,
      timestamp: activity.created_at,
      type: activity.type as OfficeReplayEvent["type"],
      fromStation,
      toStation,
      ...(agentId ? { agentId } : {}),
      ...(activity.issue_id ? { issueId: activity.issue_id } : {}),
    });
  }

  return events.sort((a, b) => b.timestamp.localeCompare(a.timestamp)).slice(0, 10);
}
