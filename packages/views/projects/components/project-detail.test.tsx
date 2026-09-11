import React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Project } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { ProjectDetail } from "./project-detail";

const mocks = vi.hoisted(() => ({
  role: "admin",
  copyText: vi.fn(),
  deleteProject: vi.fn(),
  getShareableUrl: vi.fn((path: string) => `https://app.example${path}`),
  push: vi.fn(),
  replace: vi.fn(),
  recordVisit: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  leaderChatData: undefined as
    | {
        session: { id: string };
        leader: { id: string; name: string };
      }
    | undefined,
  setActiveSession: vi.fn(),
  setSelectedAgentId: vi.fn(),
  setSelectedProjectId: vi.fn(),
  setOpen: vi.fn(),
}));

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: mocks.copyText,
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getProjectLeaderChat: vi.fn(),
  },
}));

vi.mock("@multica/core/projects", () => ({
  autonomousProjectOptions: () => ({ queryKey: ["autonomous-project"] }),
  projectLeaderChangesOptions: () => ({ queryKey: ["project-leader-changes"] }),
  projectLeaderChatOptions: () => ({ queryKey: ["project-leader-chat"] }),
  useApproveProjectLeaderChange: () => ({ mutate: vi.fn(), isPending: false }),
  useRejectProjectLeaderChange: () => ({ mutate: vi.fn(), isPending: false }),
  useResolveAutonomousEscalation: () => ({ mutate: vi.fn(), isPending: false }),
  useConfirmAutonomousTeam: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@tanstack/react-query", () => ({
  queryOptions: (options: unknown) => options,
  useQueryClient: () => ({
    invalidateQueries: vi.fn(),
  }),
  useMutation: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useQuery: (options: { queryKey?: readonly unknown[] }) => {
    switch (options.queryKey?.[0]) {
      case "project-detail":
        return { data: PROJECT, isLoading: false };
      case "members":
        return {
          data: [{ user_id: "user-1", name: "User One", role: mocks.role }],
          isLoading: false,
        };
      case "agents":
      case "pins":
        return { data: [], isLoading: false };
      case "project-leader-changes":
        return { data: { items: [] }, isLoading: false };
      case "project-leader-chat":
        return {
          data: mocks.leaderChatData,
          isLoading: false,
        };
      default:
        return { data: undefined, isLoading: false };
    }
  },
}));

vi.mock("@multica/core/projects/queries", () => ({
  projectDetailOptions: () => ({ queryKey: ["project-detail"] }),
}));

vi.mock("@multica/core/projects/mutations", () => ({
  useUpdateProject: () => ({ mutate: vi.fn() }),
  useDeleteProject: () => ({ mutate: mocks.deleteProject }),
}));

vi.mock("@multica/core/pins", () => ({
  pinListOptions: () => ({ queryKey: ["pins"] }),
  useCreatePin: () => ({ mutate: vi.fn() }),
  useDeletePin: () => ({ mutate: vi.fn() }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
  agentListOptions: () => ({ queryKey: ["agents"] }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/chat", () => ({
  useRecentContextStore: (
    selector: (state: { recordVisit: typeof mocks.recordVisit }) => unknown,
  ) => selector({ recordVisit: mocks.recordVisit }),
  useChatStore: {
    getState: () => ({
      setActiveSession: mocks.setActiveSession,
      setSelectedAgentId: mocks.setSelectedAgentId,
      setSelectedProjectId: mocks.setSelectedProjectId,
      setOpen: mocks.setOpen,
    }),
  },
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    projects: () => "/test-workspace/projects",
  }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "User One" }),
}));

vi.mock("sonner", () => ({
  toast: { success: mocks.toastSuccess, error: mocks.toastError },
}));

vi.mock("react-resizable-panels", () => ({
  useDefaultLayout: () => ({
    defaultLayout: undefined,
    onLayoutChanged: vi.fn(),
  }),
  usePanelRef: () => ({
    current: {
      isCollapsed: () => false,
      expand: vi.fn(),
      collapse: vi.fn(),
    },
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/ui/components/common/emoji-picker", () => ({
  EmojiPicker: () => null,
}));

vi.mock("@multica/ui/components/ui/resizable", () => ({
  ResizablePanelGroup: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  ResizablePanel: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  ResizableHandle: () => null,
}));

vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => (
    <>{render}</>
  ),
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
  }) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}));

vi.mock("@multica/ui/components/ui/popover", () => ({
  Popover: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  PopoverTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  PopoverContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SheetContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));

vi.mock("@multica/ui/components/ui/alert-dialog", () => ({
  AlertDialog: ({
    open,
    children,
  }: {
    open: boolean;
    children: React.ReactNode;
  }) => (open ? <div role="alertdialog">{children}</div> : null),
  AlertDialogContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  AlertDialogHeader: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  AlertDialogTitle: ({ children }: { children: React.ReactNode }) => (
    <h2>{children}</h2>
  ),
  AlertDialogDescription: ({ children }: { children: React.ReactNode }) => (
    <p>{children}</p>
  ),
  AlertDialogFooter: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  AlertDialogCancel: ({ children }: { children: React.ReactNode }) => (
    <button type="button">{children}</button>
  ),
  AlertDialogAction: ({
    children,
    onClick,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
  }) => (
    <button type="button" onClick={onClick}>
      {children}
    </button>
  ),
}));

vi.mock("../../editor", () => ({
  TitleEditor: ({ defaultValue }: { defaultValue: string }) => (
    <div>{defaultValue}</div>
  ),
  ContentEditor: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("../../issues/components/priority-icon", () => ({
  PriorityIcon: () => null,
}));

vi.mock("./project-resources-section", () => ({
  ProjectResourcesSection: () => null,
}));

vi.mock("./project-start-date-picker", () => ({
  ProjectStartDatePicker: () => null,
}));

vi.mock("./project-due-date-picker", () => ({
  ProjectDueDatePicker: () => null,
}));

vi.mock("../../issues/surface/issue-surface", () => ({
  IssueSurface: () => <div data-testid="project-issue-surface" />,
}));

vi.mock("./autonomous-control-center", () => ({
  AutonomousControlCenter: () => (
    <div data-testid="autonomous-control-center" />
  ),
}));

vi.mock("./cockpit", () => ({
  ProjectCockpitView: ({ onOpenLeaderChat }: { onOpenLeaderChat?: () => void }) => (
    <div data-testid="project-cockpit-view">
      <button type="button" onClick={onOpenLeaderChat}>
        Ask Project Manager
      </button>
    </div>
  ),
}));

vi.mock("../../layout/breadcrumb-header", () => ({
  BreadcrumbHeader: ({ actions }: { actions: React.ReactNode }) => (
    <header>{actions}</header>
  ),
}));

vi.mock("../../layout/animated-right-sidebar", () => ({
  AnimatedRightSidebar: ({ children }: { children: React.ReactNode }) => (
    <aside>{children}</aside>
  ),
  getAnimatedRightSidebarInitialOpen: () => true,
  rightSidebarPanelMotionProps: {},
  useRightSidebarShortcut: vi.fn(),
  useAnimatedRightSidebarState: () => ({
    open: true,
    visualOpen: true,
    motionEnabled: false,
    beginToggle: vi.fn(),
    handleResize: vi.fn(),
  }),
}));

const PROJECT: Project = {
  id: "project-1",
  workspace_id: "workspace-1",
  title: "Launch Plan",
  description: null,
  icon: null,
  status: "in_progress",
  priority: "high",
  lead_type: null,
  lead_id: null,
  start_date: null,
  due_date: null,
  created_at: "2026-06-01T00:00:00Z",
  updated_at: "2026-06-01T00:00:00Z",
  issue_count: 3,
  done_count: 1,
  resource_count: 0,
};

function renderProjectDetail(search = "", hash = "") {
  const adapter: NavigationAdapter = {
    push: mocks.push,
    replace: mocks.replace,
    back: vi.fn(),
    pathname: "/test-workspace/projects/project-1",
    searchParams: new URLSearchParams(search),
    hash,
    getShareableUrl: mocks.getShareableUrl,
  };

  renderWithI18n(
    <NavigationProvider value={adapter}>
      <ProjectDetail projectId={PROJECT.id} />
    </NavigationProvider>,
  );
}

beforeEach(() => {
  mocks.role = "admin";
  mocks.copyText.mockReset().mockResolvedValue(true);
  mocks.deleteProject.mockReset();
  mocks.getShareableUrl.mockClear();
  mocks.push.mockReset();
  mocks.replace.mockReset();
  mocks.recordVisit.mockReset();
  mocks.toastSuccess.mockReset();
  mocks.toastError.mockReset();
  mocks.leaderChatData = {
    session: { id: "pm-session-1" },
    leader: { id: "pm-agent-1", name: "Project Director" },
  };
  mocks.setActiveSession.mockReset();
  mocks.setSelectedAgentId.mockReset();
  mocks.setSelectedProjectId.mockReset();
  mocks.setOpen.mockReset();
});

describe("ProjectDetail content tabs", () => {
  it("renders the Cockpit view by default when no tab parameter is present", () => {
    renderProjectDetail();

    expect(screen.getByTestId("project-cockpit-view")).toBeInTheDocument();
    expect(
      screen.queryByTestId("project-issue-surface"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("autonomous-control-center"),
    ).not.toBeInTheDocument();
  });

  it("restores the Autonomous tab from the URL", () => {
    renderProjectDetail("tab=autonomous");

    expect(screen.getByTestId("autonomous-control-center")).toBeInTheDocument();
    expect(
      screen.queryByTestId("project-cockpit-view"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("project-issue-surface"),
    ).not.toBeInTheDocument();
  });

  it("stores the Autonomous tab in the URL while preserving existing location state", async () => {
    const user = userEvent.setup();
    renderProjectDetail("filter=open", "#workflow");

    await user.click(screen.getByRole("button", { name: /autonomous/i }));

    expect(mocks.replace).toHaveBeenCalledWith(
      "/test-workspace/projects/project-1?filter=open&tab=autonomous#workflow",
    );
  });

  it("stores the Issues tab in the URL when navigating to Issues", async () => {
    const user = userEvent.setup();
    renderProjectDetail("filter=open", "#workflow");

    await user.click(screen.getByRole("button", { name: /issues/i }));

    expect(mocks.replace).toHaveBeenCalledWith(
      "/test-workspace/projects/project-1?filter=open&tab=issues#workflow",
    );
  });

  it("removes the tab parameter when returning to Cockpit", async () => {
    const user = userEvent.setup();
    renderProjectDetail("filter=open&tab=autonomous", "#workflow");

    await user.click(screen.getByRole("button", { name: /cockpit/i }));

    expect(mocks.replace).toHaveBeenCalledWith(
      "/test-workspace/projects/project-1?filter=open#workflow",
    );
  });
});

describe("ProjectDetail issue surface layout", () => {
  it("provides a full-height flex column for the project issue surface", () => {
    renderProjectDetail("tab=issues");

    const surface = screen.getByTestId("project-issue-surface");
    const layout = surface.parentElement;

    expect(layout).toHaveClass(
      "flex",
      "h-full",
      "min-h-0",
      "flex-1",
      "flex-col",
    );
  });
});

describe("ProjectDetail sharing", () => {
  it("copies the platform shareable URL instead of the renderer URL", async () => {
    const user = userEvent.setup();
    renderProjectDetail();

    await user.click(screen.getByRole("button", { name: "Copy link" }));

    expect(mocks.getShareableUrl).toHaveBeenCalledWith(
      "/test-workspace/projects/project-1",
    );
    expect(mocks.copyText).toHaveBeenCalledWith(
      "https://app.example/test-workspace/projects/project-1",
    );
  });
});

describe("ProjectDetail project deletion", () => {
  it("requires confirmation and navigates only after deletion succeeds", async () => {
    const user = userEvent.setup();
    renderProjectDetail();

    await user.click(screen.getByRole("button", { name: "Delete project" }));

    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(mocks.deleteProject).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Delete" }));

    expect(mocks.deleteProject).toHaveBeenCalledWith(
      PROJECT.id,
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    expect(mocks.push).not.toHaveBeenCalled();

    const options = mocks.deleteProject.mock.calls[0]?.[1] as {
      onSuccess: () => void;
    };
    options.onSuccess();

    expect(mocks.toastSuccess).toHaveBeenCalledWith("Project deleted");
    expect(mocks.push).toHaveBeenCalledWith("/test-workspace/projects");
  });

  it("does not offer project deletion to regular members", () => {
    mocks.role = "member";

    renderProjectDetail();

    expect(
      screen.queryByRole("button", { name: "Delete project" }),
    ).not.toBeInTheDocument();
  });
});

describe("ProjectDetail inspector sidebar", () => {
  it("renders the inspector header and structural card sections", () => {
    renderProjectDetail();

    expect(screen.getByText("Inspector")).toBeInTheDocument();
    expect(screen.getByText(/Attached squad/i)).toBeInTheDocument();
    expect(screen.getByText("Properties")).toBeInTheDocument();
    expect(screen.getByText("Progress")).toBeInTheDocument();
  });

  it("opens project manager chat with dedicated PM agent and session when Ask Project Manager is clicked", async () => {
    const user = userEvent.setup();
    renderProjectDetail();

    const askPmBtn = screen.getByRole("button", { name: /ask project manager/i });
    await user.click(askPmBtn);

    expect(mocks.setActiveSession).toHaveBeenCalledWith("pm-session-1");
    expect(mocks.setSelectedAgentId).toHaveBeenCalledWith("pm-agent-1");
    expect(mocks.setSelectedProjectId).toHaveBeenCalledWith(PROJECT.id);
    expect(mocks.setOpen).toHaveBeenCalledWith(true);
  });

  it("does not fall back to a standard PM chat when the dedicated session is unavailable", async () => {
    const user = userEvent.setup();
    mocks.leaderChatData = undefined;
    renderProjectDetail();

    await user.click(screen.getByRole("button", { name: /ask project manager/i }));

    expect(mocks.setOpen).not.toHaveBeenCalled();
    expect(mocks.setSelectedAgentId).not.toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalledWith(
      "Project Manager chat is temporarily unavailable",
    );
  });
});
