// @vitest-environment node
import { describe, it, expect } from "vitest";
import { buildVirtualOfficeScene } from "./spatial-mapper";
import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  Agent,
} from "@multica/core/types";

describe("buildVirtualOfficeScene", () => {
  it("initializes 4 core stations even with empty input", () => {
    const scene = buildVirtualOfficeScene({});
    expect(scene.stations.pm_desk).toBeDefined();
    expect(scene.stations.coding_pods).toBeDefined();
    expect(scene.stations.qa_lab).toBeDefined();
    expect(scene.stations.approval_lounge).toBeDefined();

    expect(scene.stations.pm_desk.titleKey).toBe("office.zones.pm_desk");
    expect(scene.stations.coding_pods.titleKey).toBe("office.zones.coding_pods");
    expect(scene.stations.qa_lab.titleKey).toBe("office.zones.qa_lab");
    expect(scene.stations.approval_lounge.titleKey).toBe("office.zones.approval_lounge");

    expect(scene.flowEdges.length).toBeGreaterThanOrEqual(4);
    expect(scene.desks).toEqual([]);
    expect(scene.pendingApprovalsCount).toBe(0);
  });

  it("maps leader agent to PM desk and team members to coding pods and QA lab", () => {
    const mockSnapshot: AutonomousProjectSnapshot = {
      partial: false,
      missing_sections: [],
      enabled: true,
      health: {
        status: "ready",
        active_workflows: 1,
        blocked: 0,
        failed_actions: 0,
        waiting: 0,
        resumable: 0,
      },
      bootstrap: {
        autonomy_mode: "autonomous",
        autonomy_level: "delivery",
        brief: "Launch 2.5D Office",
        knowledge: [],
        policy: {},
        budget: {},
        status: "ready",
        updated_at: "",
      },
      draft: null,
      runtimes: [],
      skills: [],
      actions: [],
      activity: [],
      decisions: [],
      deployments: [],
      quality_gates: [],
      escalations: [],
      budget: null,
      brain: null,
      diagnostics: [],
      team: {
        id: "team-1",
        squad_id: "sq-1",
        leader_agent_id: "agent-pm",
        leader_name: "Ada",
        intent: "Ship 2.5D Office",
        status: "active",
        planner_name: "mika",
        planner_model: null,
        plan_revision: 2,
        last_planned_at: null,
        updated_at: "",
        members: [
          {
            role: "Frontend Engineer",
            family: "engineering",
            agent_id: "agent-dev-1",
            agent_name: "Devon",
            capabilities: [],
            responsibilities: ["Build UI components"],
            reason: "",
            active: true,
            current_task_id: null,
            current_task_title: null,
            current_task_status: null,
            created_at: "",
          },
          {
            role: "Backend Engineer",
            family: "engineering",
            agent_id: "agent-dev-2",
            agent_name: "Burt",
            capabilities: [],
            responsibilities: ["Implement API routes"],
            reason: "",
            active: true,
            current_task_id: null,
            current_task_title: null,
            current_task_status: null,
            created_at: "",
          },
          {
            role: "QA Automation Tester",
            family: "qa",
            agent_id: "agent-qa-1",
            agent_name: "Quinn",
            capabilities: [],
            responsibilities: ["E2E tests"],
            reason: "",
            active: true,
            current_task_id: null,
            current_task_title: null,
            current_task_status: null,
            created_at: "",
          },
        ],
      },
      plan: {
        id: "plan-1",
        revision: 2,
        goal: "Launch 2.5D Virtual Office",
        status: "in_progress",
        plan_digest: "digest",
        approved_at: null,
        approved_by: null,
        planner_name: "mika",
        planner_model: null,
        specification: {},
        policy: {},
        edges: [],
        updated_at: "",
        nodes: [
          {
            id: "node-1",
            key: "N1",
            is_epic: false,
            parent_epic_key: null,
            kind: "task",
            title: "Build Canvas",
            status: "in_progress",
            priority: 1,
            risk: "low",
            required_role_family: "engineering",
            assigned_role: "Frontend Engineer",
            assigned_agent_id: "agent-dev-1",
            materialized_issue_id: "iss-101",
            attempt: 1,
            max_attempts: 3,
            blocked_category: null,
            blocked_reason: null,
            issue_status: "in_progress",
            workflow_state: "running",
            acceptance_criteria: [],
            requirement_ids: [],
            updated_at: "",
          },
          {
            id: "node-2",
            key: "N2",
            is_epic: false,
            parent_epic_key: null,
            kind: "task",
            title: "Setup Routes",
            status: "completed",
            priority: 2,
            risk: "low",
            required_role_family: "engineering",
            assigned_role: "Backend Engineer",
            assigned_agent_id: "agent-dev-2",
            materialized_issue_id: null,
            attempt: 1,
            max_attempts: 3,
            blocked_category: null,
            blocked_reason: null,
            issue_status: "done",
            workflow_state: "completed",
            acceptance_criteria: [],
            requirement_ids: [],
            updated_at: "",
          },
        ],
      },
      workflows: [
        {
          id: "wf-1",
          issue_id: "iss-101",
          issue_title: "Coding Isometric Canvas",
          state: "running",
          revision: 1,
          review_cycles: 0,
          owner_agent_id: "agent-dev-1",
          owner_agent_name: "Devon",
          reviewer_agent_id: null,
          reviewer_agent_name: null,
          pending_actions: 0,
          failed_actions: 0,
          updated_at: "",
        },
      ],
      control: {
        paused: false,
        paused_at: null,
        replan_requested_at: null,
        replan_completed_at: null,
        last_error: null,
      },
    };

    const mockAgents = [
      {
        id: "agent-pm",
        name: "Ada",
        created_at: "",
        updated_at: "",
      },
      {
        id: "agent-dev-1",
        name: "Devon",
        created_at: "",
        updated_at: "",
      },
      {
        id: "agent-dev-2",
        name: "Burt",
        created_at: "",
        updated_at: "",
      },
      {
        id: "agent-qa-1",
        name: "Quinn",
        created_at: "",
        updated_at: "",
      },
    ] as Agent[];

    const scene = buildVirtualOfficeScene({
      snapshot: mockSnapshot,
      agents: mockAgents,
    });

    // PM desk desk
    const pmDesk = scene.desks.find((d) => d.stationKey === "pm_desk");
    expect(pmDesk).toBeDefined();
    expect(pmDesk?.agentId).toBe("agent-pm");
    expect(pmDesk?.agentName).toBe("Ada");

    // Coding Pods desks
    const devDesks = scene.desks.filter((d) => d.stationKey === "coding_pods");
    expect(devDesks.length).toBe(2);
    const dev1 = devDesks.find((d) => d.agentId === "agent-dev-1");
    expect(dev1?.status).toBe("running");
    expect(dev1?.currentTaskTitle).toBe("Build Canvas");

    // QA lab desk
    const qaDesks = scene.desks.filter((d) => d.stationKey === "qa_lab");
    expect(qaDesks.length).toBe(1);
    expect(qaDesks[0]?.agentId).toBe("agent-qa-1");
    expect(qaDesks[0]?.role).toBe("QA Automation Tester");

    // Flow edge active checks
    const pmToCodingEdge = scene.flowEdges.find(
      (e) => e.fromStation === "pm_desk" && e.toStation === "coding_pods",
    );
    expect(pmToCodingEdge?.active).toBe(true);

    // Completed tasks stats
    expect(scene.stats.completedTasks).toBe(1);
  });

  it("calculates pending approvals and activates approval lounge edges correctly", () => {
    const mockSnapshot: AutonomousProjectSnapshot = {
      partial: false,
      missing_sections: [],
      enabled: true,
      health: {
        status: "attention",
        active_workflows: 0,
        blocked: 1,
        failed_actions: 0,
        waiting: 1,
        resumable: 0,
      },
      bootstrap: null,
      draft: null,
      runtimes: [],
      skills: [],
      team: null,
      workflows: [],
      actions: [],
      activity: [],
      decisions: [],
      deployments: [],
      budget: null,
      brain: null,
      diagnostics: [],
      quality_gates: [],
      plan: {
        id: "plan-2",
        revision: 1,
        goal: "Autonomous Sprint",
        status: "awaiting_approval",
        plan_digest: "digest",
        approved_at: null,
        approved_by: null,
        planner_name: "mika",
        planner_model: null,
        specification: {},
        policy: {},
        nodes: [],
        edges: [],
        updated_at: "",
      },
      escalations: [
        {
          id: "esc-1",
          node_id: "iss-1",
          category: "rate_limit",
          summary: "API rate limit reached",
          status: "pending",
          severity: "high",
          context: {},
          opened_at: "",
          resolved_at: null,
        },
      ],
      control: {
        paused: false,
        paused_at: null,
        replan_requested_at: null,
        replan_completed_at: null,
        last_error: null,
      },
    };

    const mockChangeRequests: ProjectLeaderChangeRequest[] = [
      {
        id: "cr-1",
        state: "pending",
        request_text: "Add QA agent to team",
        proposal: null,
        impact: null,
        base_plan_id: "plan-1",
        applied_plan_id: "plan-2",
        error: "",
        created_at: "",
        updated_at: "",
      },
    ];

    const scene = buildVirtualOfficeScene({
      snapshot: mockSnapshot,
      changeRequests: mockChangeRequests,
    });

    // 1 (plan) + 1 (change request) + 1 (escalation) = 3
    expect(scene.pendingApprovalsCount).toBe(3);
    expect(scene.stats.pendingApprovals).toBe(3);

    const pmToApprovalEdge = scene.flowEdges.find(
      (e) => e.fromStation === "pm_desk" && e.toStation === "approval_lounge",
    );
    expect(pmToApprovalEdge?.active).toBe(true);
  });

  it("parses activity stream into spectator replay events", () => {
    const mockSnapshot: AutonomousProjectSnapshot = {
      partial: false,
      missing_sections: [],
      enabled: true,
      health: {
        status: "ready",
        active_workflows: 0,
        blocked: 0,
        failed_actions: 0,
        waiting: 0,
        resumable: 0,
      },
      bootstrap: null,
      draft: null,
      runtimes: [],
      skills: [],
      team: null,
      workflows: [],
      actions: [],
      decisions: [],
      plan: null,
      quality_gates: [],
      deployments: [],
      escalations: [],
      diagnostics: [],
      budget: null,
      brain: null,
      activity: [
        {
          id: "act-1",
          type: "task.dispatched",
          title: "Dispatched task to Devon",
          created_at: "2026-10-29T12:00:00Z",
          issue_id: "iss-101",
        },
        {
          id: "act-2",
          type: "workflow.review_requested",
          title: "Code review requested for PR #42",
          created_at: "2026-10-29T12:10:00Z",
        },
        {
          id: "act-3",
          type: "quality_gate.passed",
          title: "All E2E tests passed",
          created_at: "2026-10-29T12:15:00Z",
        },
      ],
      control: {
        paused: false,
        paused_at: null,
        replan_requested_at: null,
        replan_completed_at: null,
        last_error: null,
      },
    };

    const scene = buildVirtualOfficeScene({ snapshot: mockSnapshot });

    expect(scene.recentEvents.length).toBe(3);
    expect(scene.recentEvents[0]).toMatchObject({
      id: "act-1",
      fromStation: "pm_desk",
      toStation: "coding_pods",
      issueId: "iss-101",
    });
    expect(scene.recentEvents[1]).toMatchObject({
      id: "act-2",
      fromStation: "coding_pods",
      toStation: "qa_lab",
    });
    expect(scene.recentEvents[2]).toMatchObject({
      id: "act-3",
      fromStation: "qa_lab",
      toStation: "approval_lounge",
    });
  });

  it("calculates active and queued tasks correctly when an agent has 1 active and 3 queued tasks without overlap", () => {
    const mockSnapshot: AutonomousProjectSnapshot = {
      partial: false,
      missing_sections: [],
      enabled: true,
      health: { status: "ready", active_workflows: 1, blocked: 0, failed_actions: 0, waiting: 0, resumable: 0 },
      bootstrap: null,
      draft: null,
      runtimes: [],
      skills: [],
      actions: [],
      activity: [],
      decisions: [],
      deployments: [],
      quality_gates: [],
      escalations: [],
      budget: null,
      brain: null,
      diagnostics: [],
      team: {
        id: "team-cleaner",
        squad_id: "sq-1",
        leader_agent_id: "agent-pm",
        leader_name: "PM",
        intent: "Device Cleaner",
        status: "active",
        planner_name: "mika",
        planner_model: null,
        plan_revision: 1,
        last_planned_at: null,
        updated_at: "",
        members: [
          {
            role: "mobile_engineer",
            family: "engineering",
            agent_id: "agent-mobile",
            agent_name: "DeviceCleaner Native Mobile",
            capabilities: [],
            responsibilities: [],
            reason: "",
            active: true,
            current_task_id: null,
            current_task_title: null,
            current_task_status: null,
            created_at: "",
          },
          {
            role: "security_engineer",
            family: "engineering",
            agent_id: "agent-security",
            agent_name: "DeviceCleaner Security",
            capabilities: [],
            responsibilities: [],
            reason: "",
            active: true,
            current_task_id: null,
            current_task_title: null,
            current_task_status: null,
            created_at: "",
          },
        ],
      },
      plan: {
        id: "plan-1",
        revision: 1,
        goal: "Device Cleaner Native",
        status: "in_progress",
        plan_digest: "digest",
        approved_at: null,
        approved_by: null,
        planner_name: "mika",
        planner_model: null,
        specification: {},
        policy: {},
        edges: [],
        updated_at: "",
        nodes: [
          // 4 tasks for mobile_engineer: 1 in_progress, 3 pending/ready/blocked
          {
            id: "node-m1",
            key: "M1",
            is_epic: false,
            parent_epic_key: null,
            kind: "task",
            title: "Active: Verify Binary Size Constraints",
            status: "in_progress",
            priority: 1,
            risk: "low",
            required_role_family: "engineering",
            assigned_role: "mobile_engineer",
            assigned_agent_id: "agent-mobile",
            materialized_issue_id: "iss-1",
            attempt: 1,
            max_attempts: 3,
            blocked_category: null,
            blocked_reason: null,
            issue_status: "in_progress",
            workflow_state: "running",
            acceptance_criteria: [],
            requirement_ids: [],
            updated_at: "",
          },
          {
            id: "node-m2",
            key: "M2",
            is_epic: false,
            parent_epic_key: null,
            kind: "task",
            title: "Queued 1: Implement Camera Roll Scanner",
            status: "ready",
            priority: 2,
            risk: "low",
            required_role_family: "engineering",
            assigned_role: "mobile_engineer",
            assigned_agent_id: "agent-mobile",
            materialized_issue_id: "iss-2",
            attempt: 0,
            max_attempts: 3,
            blocked_category: null,
            blocked_reason: null,
            issue_status: "todo",
            workflow_state: null,
            acceptance_criteria: [],
            requirement_ids: [],
            updated_at: "",
          },
          {
            id: "node-m3",
            key: "M3",
            is_epic: false,
            parent_epic_key: null,
            kind: "task",
            title: "Queued 2: Build Native Compression Bridge",
            status: "pending",
            priority: 3,
            risk: "low",
            required_role_family: "engineering",
            assigned_role: "mobile_engineer",
            assigned_agent_id: "agent-mobile",
            materialized_issue_id: "iss-3",
            attempt: 0,
            max_attempts: 3,
            blocked_category: null,
            blocked_reason: null,
            issue_status: "todo",
            workflow_state: null,
            acceptance_criteria: [],
            requirement_ids: [],
            updated_at: "",
          },
          {
            id: "node-m4",
            key: "M4",
            is_epic: false,
            parent_epic_key: null,
            kind: "task",
            title: "Queued 3: Hardware Acceleration Optimizations",
            status: "blocked",
            priority: 4,
            risk: "low",
            required_role_family: "engineering",
            assigned_role: "mobile_engineer",
            assigned_agent_id: "agent-mobile",
            materialized_issue_id: "iss-4",
            attempt: 0,
            max_attempts: 3,
            blocked_category: null,
            blocked_reason: null,
            issue_status: "todo",
            workflow_state: null,
            acceptance_criteria: [],
            requirement_ids: [],
            updated_at: "",
          },
        ],
      },
      workflows: [],
      control: {
        paused: false,
        paused_at: null,
        replan_requested_at: null,
        replan_completed_at: null,
        last_error: null,
      },
    };

    const scene = buildVirtualOfficeScene({ snapshot: mockSnapshot });
    const mobileDesk = scene.desks.find((d) => d.agentId === "agent-mobile");
    const securityDesk = scene.desks.find((d) => d.agentId === "agent-security");

    expect(mobileDesk).toBeDefined();
    expect(securityDesk).toBeDefined();

    // Verify 1 active and 3 queued tasks
    expect(mobileDesk?.activeTask).toBeDefined();
    expect(mobileDesk?.activeTask?.title).toBe("Active: Verify Binary Size Constraints");
    expect(mobileDesk?.activeTask?.isCurrent).toBe(true);

    expect(mobileDesk?.queuedTasks.length).toBe(3);
    expect(mobileDesk?.queuedTasks?.[0]?.title).toBe("Queued 1: Implement Camera Roll Scanner");
    expect(mobileDesk?.queuedTasks?.[1]?.title).toBe("Queued 2: Build Native Compression Bridge");
    expect(mobileDesk?.queuedTasks?.[2]?.title).toBe("Queued 3: Hardware Acceleration Optimizations");
    expect(mobileDesk?.totalAssignedTasksCount).toBe(4);

    // Verify spatial coordinates have no overlap (distance between desks > 200px)
    const dx = Math.abs(mobileDesk!.deskCoord.x - securityDesk!.deskCoord.x);
    expect(dx).toBeGreaterThanOrEqual(200);
  });
});
