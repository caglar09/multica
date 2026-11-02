import { beforeEach, describe, it, expect, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { ProjectVirtualOfficeView } from "./project-virtual-office-view";
import { buildVirtualOfficeScene } from "../spatial-mapper";
import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  Agent,
} from "@multica/core/types";

const officeMocks = vi.hoisted(() => ({
  liveData: null as unknown,
  conversation: null as unknown,
  conversationOptions: [] as unknown[],
  send: vi.fn(async () => true),
  retry: vi.fn(async () => true),
  openInChat: vi.fn(),
  createIssue: vi.fn(async () => ({ identifier: "MC-42" })),
  rerunIssue: vi.fn(),
  retryAction: vi.fn(),
}));

vi.mock("../use-office-live-data", () => ({
  useOfficeLiveData: () => officeMocks.liveData,
}));
vi.mock("../use-office-conversation", () => ({
  useOfficeConversation: (options: unknown) => {
    officeMocks.conversationOptions.push(options);
    return officeMocks.conversation;
  },
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: undefined, isPending: false }),
}));
vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: () => "http://127.0.0.1:8080" },
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/ws/issues/${id}` }),
}));
vi.mock("@multica/core/issues/mutations", () => ({
  useCreateIssue: () => ({ mutateAsync: officeMocks.createIssue, isPending: false }),
}));
vi.mock("@multica/core/projects", () => ({
  projectReportOptions: () => ({ queryKey: ["project-report"], queryFn: async () => undefined }),
  useRerunAutonomousIssue: () => ({ mutate: officeMocks.rerunIssue, isPending: false }),
  useRetryAutonomousAction: () => ({ mutate: officeMocks.retryAction, isPending: false }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children, ...props }: React.ComponentProps<"a">) => (
    <a href={href} {...props}>{children}</a>
  ),
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
        agent_id: "agent-dev",
        issue_id: "iss-1",
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

  const mockScene = buildVirtualOfficeScene({
    snapshot: mockSnapshot,
    changeRequests: mockChangeRequests,
    agents: mockAgents,
  });

  beforeEach(() => {
    vi.clearAllMocks();
    officeMocks.conversationOptions = [];
    officeMocks.liveData = { scene: mockScene, isLoading: false, isPartial: false };
    officeMocks.conversation = {
      agentId: "agent-pm",
      sessionId: "session-1",
      messages: [{ id: "message-1", role: "assistant", content: "Saved reply", created_at: "2026-10-31T12:00:00Z" }],
      pending: null,
      taskMessages: [],
      availability: "online",
      status: "ready",
      isLoading: false,
      isSending: false,
      canSend: true,
      sendError: null,
      send: officeMocks.send,
      retry: officeMocks.retry,
      openInChat: officeMocks.openInChat,
    };
    officeMocks.send.mockReset().mockResolvedValue(true);
    officeMocks.createIssue.mockReset().mockResolvedValue({ identifier: "MC-42" });
  });

  it("renders real desks and routes the conversation to the selected agent", async () => {
    const user = userEvent.setup();
    renderWithI18n(
      <ProjectVirtualOfficeView
        projectId="project-1"
        snapshot={mockSnapshot}
        changeRequests={mockChangeRequests}
        leaderSessionId="leader-session"
        leaderAgentId="agent-pm"
        canControl
      />,
    );

    expect(screen.getAllByText("Ada").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Devon").length).toBeGreaterThanOrEqual(1);
    expect(officeMocks.conversationOptions.at(-1)).toMatchObject({
      projectId: "project-1",
      leaderSessionId: "leader-session",
      leaderAgentId: "agent-pm",
      desk: { agentId: "agent-pm", isLeader: true },
    });

    await user.selectOptions(screen.getByRole("combobox", { name: "Agent" }), "desk-agent-dev");
    await waitFor(() => expect(officeMocks.conversationOptions.at(-1)).toMatchObject({
      projectId: "project-1",
      desk: { agentId: "agent-dev" },
    }));
  });

  it("triggers onApprovePlan when clicking approve plan in inspector", async () => {
    const user = userEvent.setup();
    const onApprovePlan = vi.fn();
    renderWithI18n(
      <ProjectVirtualOfficeView
        projectId="project-1"
        snapshot={mockSnapshot}
        changeRequests={mockChangeRequests}
        leaderSessionId="leader-session"
        leaderAgentId="agent-pm"
        canControl
        onApprovePlan={onApprovePlan}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Inspector" }));
    await user.click(screen.getByRole("button", { name: "Approve Autonomous Plan" }));
    expect(onApprovePlan).toHaveBeenCalledTimes(1);
  });

  it("triggers onApproveChange when approving a leader change request", async () => {
    const user = userEvent.setup();
    const onApproveChange = vi.fn();
    renderWithI18n(
      <ProjectVirtualOfficeView
        projectId="project-1"
        snapshot={mockSnapshot}
        changeRequests={mockChangeRequests}
        leaderSessionId="leader-session"
        leaderAgentId="agent-pm"
        canControl
        onApproveChange={onApproveChange}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Inspector" }));
    expect(screen.getByText("Requesting additional reviewer agent")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Quick Approve" }));
    expect(onApproveChange).toHaveBeenCalledWith("cr-101");
  });

  it("replays only routed activity and sends messages through the conversation", async () => {
    const user = userEvent.setup();
    renderWithI18n(<ProjectVirtualOfficeView projectId="project-1" />);

    await user.click(screen.getByRole("button", { name: "Spectator Activity Feed" }));
    expect(screen.getByText("Task assigned")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Replay on Map" }));
    expect(screen.getAllByText("Task assigned").length).toBeGreaterThanOrEqual(2);

    await user.type(screen.getByLabelText("Message"), "Check the current status");
    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(officeMocks.send).toHaveBeenCalledWith("Check the current status");
    expect(await screen.findByText("Saved reply")).toBeInTheDocument();
  });

  it("sends quick prompts to the selected agent and preserves the draft", async () => {
    const user = userEvent.setup();
    renderWithI18n(<ProjectVirtualOfficeView projectId="project-1" />);

    const composer = screen.getByLabelText("Message");
    await user.type(composer, "Keep this draft");
    await user.click(screen.getByRole("button", { name: "Summarize project status" }));

    expect(officeMocks.send).toHaveBeenCalledWith("Summarize project status");
    expect(composer).toHaveValue("Keep this draft");
    expect(await screen.findByText("Saved reply")).toBeInTheDocument();
  });

  it("waits for issue creation and links to the created task", async () => {
    const user = userEvent.setup();
    let resolveIssue: ((issue: { identifier: string }) => void) | undefined;
    officeMocks.createIssue.mockImplementationOnce(
      () => new Promise((resolve) => { resolveIssue = resolve; }),
    );
    renderWithI18n(<ProjectVirtualOfficeView projectId="project-1" />);
    await user.selectOptions(screen.getByRole("combobox", { name: "Agent" }), "desk-agent-dev");
    await user.click(screen.getByRole("button", { name: "Assign task" }));
    await user.type(screen.getByLabelText("Task title"), "Review the implementation");
    await user.click(screen.getAllByRole("button", { name: "Assign task" }).at(-1)!);
    await waitFor(() => expect(officeMocks.createIssue).toHaveBeenCalledWith({
      title: "Review the implementation",
      assignee_type: "agent",
      assignee_id: "agent-dev",
      project_id: "project-1",
    }));
    expect(screen.queryByRole("link", { name: "Open task" })).not.toBeInTheDocument();
    await act(async () => resolveIssue?.({ identifier: "MC-42" }));
    expect(await screen.findByRole("link", { name: "Open task" })).toHaveAttribute("href", "/ws/issues/MC-42");
  });
});
