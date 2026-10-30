import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { ProjectVirtualOfficeView } from "./project-virtual-office-view";
import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  Agent,
} from "@multica/core/types";

vi.mock("@multica/core/api", () => ({
  api: {
    getBaseUrl: () => "http://127.0.0.1:8080",
  },
}));

describe("ProjectVirtualOfficeView", () => {
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
    bootstrap: null,
    draft: null,
    runtimes: [],
    skills: [],
    actions: [],
    decisions: [],
    deployments: [],
    budget: null,
    brain: null,
    diagnostics: [],
    quality_gates: [],
    team: {
      id: "team-1",
      squad_id: "sq-1",
      leader_agent_id: "agent-pm",
      leader_name: "Ada",
      intent: "Lead",
      status: "active",
      planner_name: "mika",
      planner_model: null,
      plan_revision: 1,
      last_planned_at: null,
      updated_at: "",
      members: [
        {
          role: "Frontend Engineer",
          family: "engineering",
          agent_id: "agent-dev",
          agent_name: "Devon",
          capabilities: [],
          responsibilities: ["Build 2.5D Canvas"],
          reason: "",
          active: true,
          current_task_id: "task-1",
          current_task_title: "Implement SVG Podiums",
          current_task_status: "in_progress",
          created_at: "",
        },
      ],
    },
    plan: {
      id: "plan-1",
      revision: 1,
      goal: "Ship Virtual Office",
      status: "awaiting_approval",
      plan_digest: "digest",
      approved_at: null,
      approved_by: null,
      planner_name: "mika",
      planner_model: null,
      specification: {},
      policy: {},
      edges: [],
      updated_at: "",
      nodes: [],
    },
    workflows: [
      {
        id: "wf-1",
        issue_id: "iss-1",
        issue_title: "Implement SVG Podiums",
        state: "running",
        revision: 1,
        review_cycles: 0,
        owner_agent_id: "agent-dev",
        owner_agent_name: "Devon",
        reviewer_agent_id: null,
        reviewer_agent_name: null,
        pending_actions: 0,
        failed_actions: 0,
        updated_at: "",
      },
    ],
    escalations: [],
    activity: [
      {
        id: "evt-1",
        type: "task.dispatched",
        title: "Dispatched canvas task to Devon",
        created_at: "2026-10-29T12:00:00Z",
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
      id: "cr-101",
      state: "pending",
      request_text: "Requesting additional reviewer agent",
      proposal: null,
      impact: null,
      base_plan_id: "plan-1",
      applied_plan_id: "plan-1",
      error: "",
      created_at: "",
      updated_at: "",
    },
  ];

  const mockAgents = [
    {
      id: "agent-pm",
      name: "Ada",
      created_at: "",
      updated_at: "",
    },
    {
      id: "agent-dev",
      name: "Devon",
      created_at: "",
      updated_at: "",
    },
  ] as Agent[];

  it("renders 4 stations, agent desks and activity items", () => {
    renderWithI18n(
      <ProjectVirtualOfficeView
        snapshot={mockSnapshot}
        changeRequests={mockChangeRequests}
        agents={mockAgents}
        canControl
      />,
    );

    // Verify 4 Zone headings on canvas
    expect(screen.getByText("Planning & PM Desk")).toBeInTheDocument();
    expect(screen.getByText("Active Coding Pods")).toBeInTheDocument();
    expect(screen.getByText("QA & Verification Lab")).toBeInTheDocument();
    expect(screen.getAllByText("Decision & Approval Lounge").length).toBeGreaterThanOrEqual(1);

    // Verify agent desks
    expect(screen.getAllByText("Ada").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Devon").length).toBeGreaterThanOrEqual(1);

    // Verify activity feed item
    expect(screen.getByText("Dispatched canvas task to Devon")).toBeInTheDocument();
  });

  it("triggers onApprovePlan when clicking approve plan in inspector", async () => {
    const user = userEvent.setup();
    const onApprovePlan = vi.fn();

    renderWithI18n(
      <ProjectVirtualOfficeView
        snapshot={mockSnapshot}
        changeRequests={mockChangeRequests}
        agents={mockAgents}
        canControl
        onApprovePlan={onApprovePlan}
      />,
    );

    // Switch to Inspector tab
    const inspectorTabButton = screen.getByRole("button", { name: /inspector/i });
    await user.click(inspectorTabButton);

    const approveButton = screen.getByRole("button", { name: "Approve Autonomous Plan" });
    await user.click(approveButton);

    expect(onApprovePlan).toHaveBeenCalledTimes(1);
  });

  it("triggers onApproveChange when approving a leader change request", async () => {
    const user = userEvent.setup();
    const onApproveChange = vi.fn();

    renderWithI18n(
      <ProjectVirtualOfficeView
        snapshot={mockSnapshot}
        changeRequests={mockChangeRequests}
        agents={mockAgents}
        canControl
        onApproveChange={onApproveChange}
      />,
    );

    // Switch to Inspector tab
    const inspectorTabButton = screen.getByRole("button", { name: /inspector/i });
    await user.click(inspectorTabButton);

    expect(screen.getByText("Requesting additional reviewer agent")).toBeInTheDocument();
    const quickApproveBtn = screen.getByRole("button", { name: "Quick Approve" });
    await user.click(quickApproveBtn);

    expect(onApproveChange).toHaveBeenCalledWith("cr-101");
  });

  it("triggers activity replay on map when replay button is clicked", async () => {
    const user = userEvent.setup();

    renderWithI18n(
      <ProjectVirtualOfficeView
        snapshot={mockSnapshot}
        changeRequests={mockChangeRequests}
        agents={mockAgents}
      />,
    );

    const replayBtn = screen.getByRole("button", { name: "Replay on Map" });
    await user.click(replayBtn);

    // Replay floating banner should appear above the canvas in addition to the feed item
    expect(screen.getAllByText("Dispatched canvas task to Devon").length).toBe(2);
  });
});
