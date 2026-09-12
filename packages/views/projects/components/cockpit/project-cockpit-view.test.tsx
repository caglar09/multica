import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type {
  Project,
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
} from "@multica/core/types";
import { renderWithI18n } from "../../../test/i18n";
import { ProjectCockpitView } from "./project-cockpit-view";

vi.mock("@multica/core/projects", () => ({
  usePauseAutonomousProject: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useResumeAutonomousProject: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useReplanAutonomousProject: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/workspace/agents/${id}`,
    agents: () => "/workspace/agents",
  }),
}));

vi.mock("../../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
  }),
}));

vi.mock("../../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="actor-avatar" />,
}));

const MOCK_PROJECT: Project = {
  id: "project-1",
  workspace_id: "workspace-1",
  title: "E-Commerce Core Overhaul",
  description: "Modernizing payment processing & autonomous squad orchestration",
  icon: null,
  status: "in_progress",
  priority: "high",
  lead_type: "agent",
  lead_id: "agent-lead-1",
  start_date: null,
  due_date: null,
  created_at: "2026-06-01T00:00:00Z",
  updated_at: "2026-06-01T00:00:00Z",
  issue_count: 10,
  done_count: 6,
  resource_count: 2,
};

const MOCK_SNAPSHOT = {
  project_id: "project-1",
  workspace_id: "workspace-1",
  enabled: true,
  health: {
    status: "running",
    active_workflows: 2,
    failing_workflows: 0,
    blocked_workflows: 0,
    recent_errors: [],
  },
  control: {
    paused: false,
  },
  team: {
    members: [
      {
        agent_id: "agent-lead-1",
        agent_name: "Mika (Lead)",
        role: "lead",
        family: "general",
        capabilities: ["planning"],
        responsibilities: ["Lead squad"],
        reason: "Lead agent",
        active: true,
        current_task_id: "task-1",
        current_task_title: "Sprint planning & architecture review",
        current_task_status: "in_progress",
        created_at: "2026-06-01T00:00:00Z",
      },
      {
        agent_id: "agent-dev-1",
        agent_name: "Nexus (Backend)",
        role: "backend",
        family: "coding",
        capabilities: ["go", "sql"],
        responsibilities: ["Backend tasks"],
        reason: "Dev agent",
        active: true,
        current_task_id: "task-2",
        current_task_title: "Optimizing checkout idempotency keys",
        current_task_status: "running",
        created_at: "2026-06-01T00:00:00Z",
      },
    ],
  },
  stage: "execution",
  active_task: {
    task_id: "task-101",
    title: "Implement Stripe Webhook Idempotency",
    agent_id: "agent-dev-1",
    status: "running",
  },
  budget: {
    tokens_used: 125400,
    cost_microunits_used: 4800000,
    runtime_seconds_used: 4320,
    token_limit: 500000,
    runtime_seconds_limit: 86400,
    cost_microunits_limit: 50000000,
    max_parallel_nodes: 5,
    max_total_attempts: 20,
    total_attempts: 8,
  },
  brain: {
    enabled: true,
    runtime_mode: "inherit_mika",
    runtime_id: null,
    model: null,
    thinking_level: null,
    service_tier: null,
    active_memories: 18,
    superseded_memories: 4,
    pending_learning_jobs: 0,
    deferred_learning_jobs: 0,
    learning_mode: "adaptive",
  },
  quality_gates: [
    { id: "qg-1", name: "TypeScript Check", status: "passed" },
    { id: "qg-2", name: "Unit Tests", status: "passed" },
    { id: "qg-3", name: "Security Audit", status: "passed" },
  ],
  escalations: [],
  activity: [
    {
      id: "act-1",
      type: "task.started",
      title: "Implement Stripe Webhook Idempotency",
      phase: "execution",
      status: "running",
      created_at: "2026-06-01T02:00:00Z",
    },
  ],
  recent_events: [
    {
      id: "ev-1",
      kind: "task_completed",
      title: "Completed auth schema validation",
      created_at: "2026-06-01T02:00:00Z",
    },
  ],
} as unknown as AutonomousProjectSnapshot;

const MOCK_CHANGES: ProjectLeaderChangeRequest[] = [
  {
    id: "change-1",
    project_id: "project-1",
    workspace_id: "workspace-1",
    actor_id: "agent-lead-1",
    request_text: "Add Caching Layer to Product Catalog",
    state: "approval_required",
    created_at: "2026-06-01T02:30:00Z",
  } as unknown as ProjectLeaderChangeRequest,
];

describe("ProjectCockpitView", () => {
  it("renders all six cockpit widgets", () => {
    const onNavigateTab = vi.fn();
    const onOpenLeaderChat = vi.fn();

    renderWithI18n(
      <ProjectCockpitView
        project={MOCK_PROJECT}
        snapshot={MOCK_SNAPSHOT}
        changeRequests={MOCK_CHANGES}
        canControl={true}
        onNavigateTab={onNavigateTab}
        onOpenLeaderChat={onOpenLeaderChat}
      />,
    );

    // 1. Widget 1: Live Squad
    expect(screen.getByText("Autonomous Squad")).toBeInTheDocument();
    expect(screen.getByText("Mika (Lead)")).toBeInTheDocument();
    expect(screen.getByText("Nexus (Backend)")).toBeInTheDocument();

    // 2. Widget 2: Execution Loop & Feed
    expect(screen.getByText("Execution Loop & Live Feed")).toBeInTheDocument();
    expect(screen.getByText("Implement Stripe Webhook Idempotency")).toBeInTheDocument();

    // 3. Widget 3: Decision Gate (Human in the Loop)
    expect(screen.getByText("Human-in-the-Loop & Approvals")).toBeInTheDocument();
    expect(screen.getByText("Add Caching Layer to Product Catalog")).toBeInTheDocument();

    // 4. Widget 4: Issues Velocity
    expect(screen.getByText("Sprint & Issue Velocity")).toBeInTheDocument();
    expect(screen.getByText("60%")).toBeInTheDocument();

    // 5. Widget 5: Telemetry & Budget
    expect(screen.getByText("Telemetry & Budget")).toBeInTheDocument();
    expect(screen.getAllByText(/125(\.4)?k/i).length).toBeGreaterThan(0);
    expect(screen.getByText("$4.80")).toBeInTheDocument();

    // 6. Widget 6: Brain & Quality
    expect(screen.getByText("Project Brain & Quality")).toBeInTheDocument();
    expect(screen.getByText("18")).toBeInTheDocument();
    expect(screen.getByText("3 / 3 (100%)")).toBeInTheDocument();
  });

  it("handles decision approval and tab navigation triggers", async () => {
    const user = userEvent.setup();
    const onApproveChange = vi.fn();
    const onRejectChange = vi.fn();
    const onNavigateTab = vi.fn();
    const onOpenLeaderChat = vi.fn();

    renderWithI18n(
      <ProjectCockpitView
        project={MOCK_PROJECT}
        snapshot={MOCK_SNAPSHOT}
        changeRequests={MOCK_CHANGES}
        canControl={true}
        onNavigateTab={onNavigateTab}
        onOpenLeaderChat={onOpenLeaderChat}
        onApproveChange={onApproveChange}
        onRejectChange={onRejectChange}
      />,
    );

    // Click Approve on decision card
    const approveButton = screen.getByRole("button", { name: "Approve" });
    await user.click(approveButton);
    expect(onApproveChange).toHaveBeenCalledWith("change-1");

    // Click View All Issues
    const viewIssuesButton = screen.getByRole("button", { name: /view all issues/i });
    await user.click(viewIssuesButton);
    expect(onNavigateTab).toHaveBeenCalledWith("issues");

  });

  it("renders squad bootstrapping proposal card when snapshot.draft is present and handles confirmation", async () => {
    const user = userEvent.setup();
    const onConfirmTeam = vi.fn();

    const mockDraftSnapshot = {
      ...MOCK_SNAPSHOT,
      draft: {
        status: "awaiting_configuration",
        planner_name: "Mika",
        planner_model: "claude-3-7-sonnet",
        default_runtime_id: "rt-1",
        default_skill_ids: [],
        created_at: "2026-06-01T00:00:00Z",
        updated_at: "2026-06-01T00:00:00Z",
        plan: {
          intent: "Bootstrap team",
          roles: [
            {
              role: "lead",
              family: "general",
              display_name: "Lead Architect",
              responsibilities: ["Technical strategy", "Squad review"],
              capabilities: ["architecture", "planning"],
              required_skills: [],
              reason: "Overall technical lead",
            },
            {
              role: "backend",
              family: "coding",
              display_name: "Senior Backend Engineer",
              responsibilities: ["API development", "Database migrations"],
              capabilities: ["golang", "postgres"],
              required_skills: [],
              reason: "Core backend development",
            },
          ],
        },
      },
      runtimes: [
        {
          id: "rt-1",
          name: "Default Local Runner",
          provider: "local",
          runtime_mode: "standard",
          status: "online",
        },
      ],
    } as unknown as AutonomousProjectSnapshot;

    renderWithI18n(
      <ProjectCockpitView
        project={MOCK_PROJECT}
        snapshot={mockDraftSnapshot}
        changeRequests={[]}
        canControl={true}
        onNavigateTab={vi.fn()}
        onOpenLeaderChat={vi.fn()}
        onConfirmTeam={onConfirmTeam}
      />,
    );

    // Bootstrapping Proposal Header & Roles
    expect(screen.getByText("Autonomous Squad Proposal")).toBeInTheDocument();
    expect(screen.getByText("Lead Architect")).toBeInTheDocument();
    expect(screen.getByText("Senior Backend Engineer")).toBeInTheDocument();

    // Confirm action
    const confirmButton = screen.getByRole("button", {
      name: /confirm & launch squad/i,
    });
    await user.click(confirmButton);

    expect(onConfirmTeam).toHaveBeenCalledTimes(1);
    expect(onConfirmTeam).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({ role: "lead", runtime_id: "rt-1" }),
        expect.objectContaining({ role: "backend", runtime_id: "rt-1" }),
      ]),
    );
  });
});
