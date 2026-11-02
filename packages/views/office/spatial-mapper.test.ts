// @vitest-environment node
import { describe, expect, it } from "vitest";
import type {
  Agent,
  AgentTask,
  AutonomousProjectSnapshot,
  Issue,
  TaskMessagePayload,
} from "@multica/core/types";
import { buildVirtualOfficeScene } from "./spatial-mapper";

function agent(id: string, values: Partial<Agent> = {}): Agent {
  return {
    id,
    workspace_id: "ws-1",
    runtime_id: `runtime-${id}`,
    runtime_bound: true,
    name: id === "agent-1" ? "Ada" : "Bea",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    visibility: "workspace",
    permission_mode: "public_to",
    invocation_targets: [],
    status: "idle",
    max_concurrent_tasks: 2,
    model: "model",
    owner_id: null,
    skills: [],
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    archived_at: null,
    archived_by: null,
    ...values,
  };
}

function task(id: string, agentId: string, issueId: string, status: AgentTask["status"], values: Partial<AgentTask> = {}): AgentTask {
  return {
    id,
    agent_id: agentId,
    runtime_id: `runtime-${agentId}`,
    issue_id: issueId,
    status,
    priority: 1,
    dispatched_at: null,
    started_at: status === "running" ? "2026-10-31T12:00:00.000Z" : null,
    completed_at: status === "failed" ? "2026-10-31T11:00:00.000Z" : null,
    result: null,
    error: null,
    created_at: "2026-10-31T10:00:00.000Z",
    ...values,
  } as AgentTask;
}

function issue(id: string, values: Partial<Issue> = {}): Issue {
  return {
    id,
    workspace_id: "ws-1",
    number: id === "issue-1" ? 1 : 2,
    identifier: id === "issue-1" ? "MC-1" : "MC-2",
    title: id === "issue-1" ? "Build the desk" : "Review the desk",
    description: null,
    status: "in_progress",
    priority: "medium",
    assignee_type: "agent",
    assignee_id: id === "issue-1" ? "agent-1" : "agent-2",
    creator_type: "member",
    creator_id: "member-1",
    parent_issue_id: null,
    project_id: "project-1",
    position: 0,
    stage: null,
    start_date: null,
    due_date: null,
    metadata: {},
    properties: {},
    created_at: "2026-10-31T10:00:00.000Z",
    updated_at: "2026-10-31T10:00:00.000Z",
    ...values,
  } as Issue;
}

function snapshot(): AutonomousProjectSnapshot {
  return {
    partial: false,
    missing_sections: [],
    enabled: true,
    team: {
      id: "team-1",
      squad_id: "squad-1",
      leader_agent_id: "agent-1",
      leader_name: "Ada",
      intent: "Ship the office",
      status: "active",
      planner_name: "planner",
      planner_model: null,
      plan_revision: 1,
      last_planned_at: null,
      updated_at: "2026-10-31T10:00:00.000Z",
      members: [
        {
          role: "Backend Engineer",
          family: "engineering",
          agent_id: "agent-1",
          agent_name: "Ada",
          capabilities: [],
          responsibilities: [],
          reason: "",
          active: true,
          current_task_id: "run-1",
          current_task_title: "Build the desk",
          current_task_status: "running",
          created_at: "2026-10-31T10:00:00.000Z",
        },
        {
          role: "QA",
          family: "qa",
          agent_id: "agent-2",
          agent_name: "Bea",
          capabilities: [],
          responsibilities: [],
          reason: "",
          active: true,
          current_task_id: null,
          current_task_title: null,
          current_task_status: null,
          created_at: "2026-10-31T10:00:00.000Z",
        },
      ],
    },
    workflows: [{
      id: "workflow-1",
      issue_id: "issue-2",
      issue_title: "Review the desk",
      state: "reviewing",
      revision: 1,
      review_cycles: 2,
      owner_agent_id: "agent-1",
      owner_agent_name: "Ada",
      reviewer_agent_id: "agent-2",
      reviewer_agent_name: "Bea",
      pending_actions: 0,
      failed_actions: 0,
      updated_at: "2026-10-31T12:00:00.000Z",
    }],
    plan: {
      id: "plan-1",
      revision: 1,
      goal: "Ship the office",
      status: "in_progress",
      plan_digest: "digest",
      approved_at: "2026-10-31T10:00:00.000Z",
      approved_by: "member-1",
      planner_name: "planner",
      planner_model: null,
      specification: {},
      policy: {},
      nodes: [
        { id: "node-1", key: "N1", is_epic: false, parent_epic_key: null, kind: "task", title: "Build the desk", status: "in_progress", priority: 1, risk: "low", required_role_family: "engineering", assigned_role: "Backend Engineer", assigned_agent_id: "agent-1", materialized_issue_id: "issue-1", attempt: 1, max_attempts: 3, blocked_category: null, blocked_reason: null, issue_status: "in_progress", workflow_state: "running", acceptance_criteria: [], requirement_ids: [], updated_at: "2026-10-31T12:00:00.000Z" },
        { id: "node-2", key: "N2", is_epic: false, parent_epic_key: null, kind: "task", title: "Review the desk", status: "blocked", priority: 2, risk: "low", required_role_family: "qa", assigned_role: "QA", assigned_agent_id: "agent-2", materialized_issue_id: "issue-2", attempt: 1, max_attempts: 3, blocked_category: "dependency", blocked_reason: "Wait for the first task", issue_status: "blocked", workflow_state: "blocked", acceptance_criteria: [], requirement_ids: [], updated_at: "2026-10-31T12:00:00.000Z" },
      ],
      edges: [{ from: "N1", to: "N2", type: "hard" }],
      updated_at: "2026-10-31T12:00:00.000Z",
    },
    activity: [
      { id: "activity-dispatch", type: "task.dispatched", issue_id: "issue-1", agent_id: "agent-1", created_at: "2026-10-31T12:00:00.000Z", title: "Dispatch" },
      { id: "activity-review", type: "workflow.review_requested", issue_id: "issue-2", agent_id: "agent-1", created_at: "2026-10-31T12:01:00.000Z", title: "Review requested" },
      { id: "activity-unknown", type: "agent.started", issue_id: "issue-1", agent_id: "agent-1", created_at: "2026-10-31T12:02:00.000Z", title: "Unknown" },
      { id: "activity-no-route", type: "task.failed", issue_id: "issue-1", agent_id: "missing-agent", created_at: "2026-10-31T12:03:00.000Z", title: "No mapped route" },
    ],
    escalations: [],
    quality_gates: [],
    actions: [],
    diagnostics: [],
    runtimes: [],
    skills: [],
    decisions: [],
    deployments: [],
    budget: null,
    brain: null,
    bootstrap: null,
    draft: null,
    health: { status: "running", active_workflows: 1, blocked: 1, failed_actions: 0, waiting: 1, resumable: 0 },
    control: { paused: false, paused_at: null, cancelled_at: null, replan_requested_at: null, replan_completed_at: null, last_error: null },
  } as AutonomousProjectSnapshot;
}

describe("buildVirtualOfficeScene", () => {
  it("does not create fallback agents, activity, tools, bubbles, or flow edges", () => {
    const scene = buildVirtualOfficeScene({});
    expect(scene.desks).toEqual([]);
    expect(scene.flowEdges).toEqual([]);
    expect(scene.blockers).toEqual([]);
    expect(scene.recentEvents).toEqual([]);
    expect(scene.speechBubbles).toEqual([]);
    expect(scene.stations.pm_desk.titleKey).toBe("projects.office.zones.pm_desk");
    expect(scene.stations.pm_desk.metrics).toEqual([]);
  });

  it("maps current and queued runs, tools, skills, bubbles, blockers, handoffs, and replay from records", () => {
    const messages: TaskMessagePayload[] = [
      { task_id: "run-1", issue_id: "issue-1", seq: 1, type: "text", content: "Starting the implementation.", created_at: "2026-10-31T12:00:01.000Z" },
      { task_id: "run-1", issue_id: "issue-1", seq: 2, type: "tool_use", call_id: "call-1", tool: "Bash", created_at: "2026-10-31T12:00:02.000Z" },
      { task_id: "run-1", issue_id: "issue-1", seq: 3, type: "tool_result", call_id: "call-1", tool: "Bash", created_at: "2026-10-31T12:00:03.000Z" },
      { task_id: "run-1", issue_id: "issue-1", seq: 4, type: "tool_use", call_id: "call-3", tool: "Skill", input: { skill: "code-mentor" }, created_at: "2026-10-31T12:00:04.000Z" },
      { task_id: "run-1", issue_id: "issue-1", seq: 5, type: "tool_use", call_id: "call-2", tool: "Edit", created_at: "2026-10-31T12:00:05.000Z" },
      { task_id: "run-1", issue_id: "issue-1", seq: 6, type: "thinking", content: "I should update the component.", created_at: "2026-10-31T12:00:06.000Z" },
    ];
    const blockedIssue = issue("issue-2", {
      status: "blocked",
      is_blocked: true,
      unresolved_blocker_count: 1,
      blocked_by: [{ id: "dependency-1", type: "blocked_by", issue_id: "issue-2", depends_on_issue_id: "issue-1", related_issue: { id: "issue-1", identifier: "MC-1", title: "Build the desk", status: "in_progress", done: false } }],
    });
    const scene = buildVirtualOfficeScene({
      snapshot: snapshot(),
      agents: [
        agent("agent-1", { status: "working", description: "Backend", skills: [{ id: "skill-1", name: "code-mentor", description: "", enabled: true }] }),
        agent("agent-2", { status: "error", description: "Quality assurance" }),
      ],
      tasks: [
        task("run-1", "agent-1", "issue-1", "running"),
        task("queued-1", "agent-2", "issue-2", "queued"),
        task("failed-1", "agent-2", "issue-2", "failed"),
      ],
      issues: [issue("issue-1"), blockedIssue],
      presenceByAgent: new Map([
        ["agent-1", { availability: "online", workload: "working", runningCount: 1, queuedCount: 0, capacity: 2 }],
        ["agent-2", { availability: "offline", workload: "queued", runningCount: 0, queuedCount: 1, capacity: 2 }],
      ]),
      taskMessages: messages,
    });

    const ada = scene.desks.find((desk) => desk.agentId === "agent-1");
    const bea = scene.desks.find((desk) => desk.agentId === "agent-2");
    expect(ada).toMatchObject({
      status: "running",
      currentIssueId: "issue-1",
      currentTaskTitle: "Build the desk",
      activeTask: { id: "run-1", status: "running", isCurrent: true },
      isDaemonActive: true,
      allowedSkills: ["code-mentor"],
      activeSkill: { name: "code-mentor" },
      activeTool: { name: "Edit", status: "running" },
      recentTools: [
        { name: "Bash", status: "completed" },
        { name: "Skill", status: "running" },
        { name: "Edit", status: "running" },
      ],
      activeSpeechBubble: {
        id: "run-1:6",
        deskId: "desk-agent-1",
        text: "I should update the component.",
        kind: "thought",
      },
      stationKey: "pm_desk",
    });
    expect(bea).toMatchObject({
      status: "failed",
      isDaemonActive: false,
      queuedTasks: [{ id: "queued-1", title: "Review the desk", status: "queued" }],
    });
    expect(scene.flowEdges.map((item) => item.kind)).toEqual(expect.arrayContaining([
      "task_dispatch",
      "review_handoff",
      "plan_dependency",
      "issue_dependency",
    ]));
    expect(scene.blockers).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: "issue_dependency", issueId: "issue-2", params: { dependencyCount: 1 }, action: { kind: "open_issue", targetId: "issue-2" } }),
      expect.objectContaining({ kind: "plan_dependency", reasonKey: "projects.office.blockers.reasons.plan_dependency" }),
      expect.objectContaining({ kind: "failed_run", runId: "failed-1", action: { kind: "retry_run", targetId: "failed-1" } }),
      expect.objectContaining({ kind: "runtime_unavailable", agentId: "agent-2" }),
    ]));
      expect(scene.recentEvents).toEqual([
      expect.objectContaining({ id: "activity-review", type: "workflow.review_requested", fromStation: "pm_desk", toStation: "qa_lab" }),
      expect.objectContaining({ id: "activity-dispatch", type: "task.dispatched", fromStation: "pm_desk", toStation: "pm_desk" }),
    ]);
    expect(scene.speechBubbles).toHaveLength(1);
    expect(scene.stats).toMatchObject({ activeAgents: 1, runningTasks: 1 });
  });

  it("omits activity when its type or station route cannot be resolved", () => {
    const data = snapshot();
    data.activity = [
      { id: "unknown", type: "future.activity", created_at: "2026-10-31T12:00:00.000Z", title: "Unknown" },
      { id: "missing-agent", type: "task.dispatched", agent_id: "unmapped", created_at: "2026-10-31T12:01:00.000Z", title: "Unmapped" },
    ];
    const scene = buildVirtualOfficeScene({ snapshot: data, agents: [agent("agent-1")] });
    expect(scene.recentEvents).toEqual([]);
  });

  it("keeps retry actions distinct when several failed actions share a workflow", () => {
    const data = snapshot();
    data.actions = [
      { id: "action-1", run_id: "workflow-1", action_type: "check", status: "failed", attempts: 1, max_attempts: 2, last_error: null, created_at: "2026-10-31T12:00:00.000Z", updated_at: "2026-10-31T12:00:00.000Z" },
      { id: "action-2", run_id: "workflow-1", action_type: "build", status: "error", attempts: 1, max_attempts: 2, last_error: null, created_at: "2026-10-31T12:01:00.000Z", updated_at: "2026-10-31T12:01:00.000Z" },
    ];

    const scene = buildVirtualOfficeScene({ snapshot: data });
    expect(scene.blockers.filter((item) => item.action.kind === "retry_action")).toEqual([
      expect.objectContaining({ runId: "workflow-1", action: { kind: "retry_action", targetId: "action-1" } }),
      expect.objectContaining({ runId: "workflow-1", action: { kind: "retry_action", targetId: "action-2" } }),
    ]);
  });
});
