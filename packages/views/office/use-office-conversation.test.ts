import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, ChatSession } from "@multica/core/types";
import type { OfficeAgentDesk } from "./types";

const h = vi.hoisted(() => ({
  agents: [] as unknown[],
  members: [] as unknown[],
  sessions: [] as unknown[],
  messages: {} as Record<string, unknown[]>,
  pending: {} as Record<string, unknown>,
  availability: "online" as string,
  permissionAllowed: true,
  createSession: vi.fn(),
  sendMessage: vi.fn(),
  setActiveSession: vi.fn(),
  setSelectedAgentId: vi.fn(),
  setSelectedProjectId: vi.fn(),
  setOpen: vi.fn(),
  clientMessageId: "client-message-1",
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (select: (state: { user: { id: string } }) => unknown) =>
    select({ user: { id: "user-1" } }),
}));
vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => h.agents }),
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => h.members }),
}));
vi.mock("@multica/core/permissions", () => ({
  canAssignAgentToIssue: () => ({
    allowed: h.permissionAllowed,
    reason: h.permissionAllowed ? "allowed" : "private_visibility",
    message: "",
  }),
}));
vi.mock("@multica/core/agents", () => ({
  isAgentRuntimeBound: (agent: { runtime_id?: string; runtime_bound?: boolean }) =>
    Boolean(agent.runtime_id && agent.runtime_bound !== false),
  useAgentPresenceDetail: () => ({ availability: h.availability }),
}));
vi.mock("@multica/core/chat", () => ({
  useChatStore: Object.assign(() => null, {
    getState: () => ({
      setActiveSession: h.setActiveSession,
      setSelectedAgentId: h.setSelectedAgentId,
      setSelectedProjectId: h.setSelectedProjectId,
      setOpen: h.setOpen,
    }),
  }),
}));
vi.mock("@multica/core/api", () => ({
  api: {
    createChatSession: h.createSession,
    sendChatMessage: h.sendMessage,
  },
  dispatchReasonCode: (error: unknown) =>
    error && typeof error === "object" && "reasonCode" in error
      ? (error as { reasonCode?: string }).reasonCode
      : undefined,
}));
vi.mock("@multica/core/chat/queries", () => ({
  chatKeys: {
    sessions: (wsId: string) => ["chat", wsId, "sessions"],
    messages: (sessionId: string) => ["chat", "messages", sessionId],
    messagesPage: (sessionId: string) => ["chat", "messages-page", sessionId],
    pendingTask: (sessionId: string) => ["chat", "pending-task", sessionId],
  },
  chatSessionsOptions: (wsId: string) => ({
    queryKey: ["chat", wsId, "sessions"],
    queryFn: async () => h.sessions,
  }),
  chatMessagesOptions: (sessionId: string) => ({
    queryKey: ["chat", "messages", sessionId],
    queryFn: async () => h.messages[sessionId] ?? [],
  }),
  pendingChatTaskOptions: (sessionId: string) => ({
    queryKey: ["chat", "pending-task", sessionId],
    queryFn: async () => h.pending[sessionId] ?? {},
  }),
  sortChatSessions: (sessions: { updated_at?: string }[]) =>
    [...sessions].sort((a, b) => (b.updated_at ?? "").localeCompare(a.updated_at ?? "")),
  useTaskMessages: () => ({ data: [] }),
}));
vi.mock("@multica/core/chat/pending", () => ({
  enqueuePendingChatTask: (current: Record<string, unknown> | undefined, task: Record<string, unknown>, queued?: boolean) =>
    queued
      ? { ...current, queued_tasks: [...((current?.queued_tasks as unknown[]) ?? []), task] }
      : { ...task, supports_queue: current?.supports_queue, queued_tasks: [] },
  hideQueuedChatMessages: (messages: unknown[]) => messages,
}));

import { api } from "@multica/core/api";
import { useOfficeConversation } from "./use-office-conversation";

function makeDesk(agentId = "agent-1", overrides: Partial<OfficeAgentDesk> = {}): OfficeAgentDesk {
  return {
    id: agentId,
    agentId,
    agentName: "Agent One",
    role: "agent",
    status: "idle",
    queuedTasks: [],
    completedTasksCount: 0,
    totalAssignedTasksCount: 0,
    stationKey: "coding_pods",
    deskCoord: { x: 0, y: 0 },
    rawAgent: {
      id: agentId,
      name: "Agent One",
      runtime_id: "runtime-1",
      runtime_bound: true,
    } as Agent,
    ...overrides,
  };
}

function makeSession(
  id: string,
  agentId: string,
  projectId: string | null,
  status: "active" | "archived" = "active",
): ChatSession {
  return {
    id,
    agent_id: agentId,
    project_id: projectId,
    status,
    title: id,
    workspace_id: "ws-1",
    creator_id: "user-1",
    has_unread: false,
    unread_count: 0,
    last_message: null,
    pinned: false,
    created_at: "2026-10-01T00:00:00.000Z",
    updated_at: "2026-10-01T00:00:00.000Z",
  } as ChatSession;
}

function renderConversation(options: {
  projectId: string | null;
  desk: OfficeAgentDesk | null;
  leaderSessionId?: string | null;
  leaderAgentId?: string | null;
}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: queryClient }, children);
  return renderHook(() => useOfficeConversation(options), { wrapper });
}

describe("useOfficeConversation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal("crypto", { randomUUID: () => h.clientMessageId });
    h.agents = [
      { id: "agent-1", runtime_id: "runtime-1", runtime_bound: true },
      { id: "agent-2", runtime_id: "runtime-2", runtime_bound: true },
    ];
    h.members = [{ user_id: "user-1", role: "admin" }];
    h.sessions = [];
    h.messages = {};
    h.pending = {};
    h.availability = "online";
    h.permissionAllowed = true;
    vi.mocked(api.createChatSession).mockResolvedValue({ id: "session-new" } as never);
    vi.mocked(api.sendChatMessage).mockResolvedValue({
      message_id: "message-1",
      task_id: "task-1",
      created_at: "2026-10-31T00:00:00.000Z",
      supports_queue: true,
    } as never);
  });

  it("routes sends to the selected desk's matching agent and project session", async () => {
    h.sessions = [
      makeSession("wrong-agent", "agent-2", "project-1"),
      makeSession("wrong-project", "agent-1", "project-2"),
      makeSession("right-session", "agent-1", "project-1"),
    ];
    vi.mocked(api.sendChatMessage).mockImplementation(async (sessionId, content) => {
      h.messages[sessionId] = [
        {
          id: "message-1",
          chat_session_id: sessionId,
          role: "user",
          content,
          task_id: "task-1",
          created_at: "2026-10-31T00:00:00.000Z",
        },
      ];
      return {
        message_id: "message-1",
        task_id: "task-1",
        created_at: "2026-10-31T00:00:00.000Z",
        supports_queue: true,
      } as never;
    });
    const { result } = renderConversation({ projectId: "project-1", desk: makeDesk() });
    await waitFor(() => expect(result.current.status).toBe("ready"));

    await act(async () => {
      await result.current.send("Help with this issue");
    });

    expect(api.createChatSession).not.toHaveBeenCalled();
    expect(api.sendChatMessage).toHaveBeenCalledWith(
      "right-session",
      "Help with this issue",
      undefined,
      "client-message-1",
    );
    expect(result.current.messages).toEqual(
      expect.arrayContaining([expect.objectContaining({ id: "message-1", role: "user" })]),
    );
  });

  it("switches the chat session when another desk is selected", async () => {
    h.sessions = [
      makeSession("agent-1-session", "agent-1", "project-1"),
      makeSession("agent-2-session", "agent-2", "project-1"),
    ];
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: queryClient }, children);
    const { result, rerender } = renderHook(
      ({ desk }: { desk: OfficeAgentDesk }) =>
        useOfficeConversation({ projectId: "project-1", desk }),
      { initialProps: { desk: makeDesk("agent-1") }, wrapper },
    );
    await waitFor(() => expect(result.current.sessionId).toBe("agent-1-session"));
    await act(async () => {
      await result.current.send("First agent");
    });

    rerender({ desk: makeDesk("agent-2") });
    await waitFor(() => expect(result.current.sessionId).toBe("agent-2-session"));
    await act(async () => {
      await result.current.send("Second agent");
    });

    expect(api.sendChatMessage).toHaveBeenNthCalledWith(
      1,
      "agent-1-session",
      "First agent",
      undefined,
      "client-message-1",
    );
    expect(api.sendChatMessage).toHaveBeenNthCalledWith(
      2,
      "agent-2-session",
      "Second agent",
      undefined,
      "client-message-1",
    );
  });

  it("creates a direct session lazily and retries with the same session and message ID", async () => {
    vi.mocked(api.sendChatMessage)
      .mockRejectedValueOnce(new Error("temporary network failure"))
      .mockResolvedValueOnce({
        message_id: "message-1",
        task_id: "task-1",
        created_at: "2026-10-31T00:00:00.000Z",
      } as never);
    const { result } = renderConversation({ projectId: "project-1", desk: makeDesk() });
    await waitFor(() => expect(result.current.status).toBe("ready"));

    expect(api.createChatSession).not.toHaveBeenCalled();
    await act(async () => {
      await result.current.send("Retry this message");
    });
    expect(result.current.sendError?.kind).toBe("failed");

    await act(async () => {
      await result.current.retry();
    });

    expect(api.createChatSession).toHaveBeenCalledTimes(1);
    expect(api.createChatSession).toHaveBeenCalledWith({
      agent_id: "agent-1",
      project_id: "project-1",
      title: "Retry this message",
    });
    expect(api.sendChatMessage).toHaveBeenNthCalledWith(
      1,
      "session-new",
      "Retry this message",
      undefined,
      "client-message-1",
    );
    expect(api.sendChatMessage).toHaveBeenNthCalledWith(
      2,
      "session-new",
      "Retry this message",
      undefined,
      "client-message-1",
    );
    await waitFor(() => expect(result.current.sendError).toBeNull());
  });

  it("uses a direct project chat instead of the leader planning session", async () => {
    const leader = makeDesk("leader-1", { isLeader: true });
    h.sessions = [
      makeSession("leader-session", "leader-1", "project-1"),
      makeSession("direct-session", "leader-1", "project-1"),
    ];
    const { result } = renderConversation({
      projectId: "project-1",
      desk: leader,
      leaderSessionId: "leader-session",
      leaderAgentId: "leader-1",
    });
    await waitFor(() => expect(result.current.status).toBe("ready"));

    await act(async () => {
      await result.current.send("Project status?");
    });

    expect(api.createChatSession).not.toHaveBeenCalled();
    expect(api.sendChatMessage).toHaveBeenCalledWith(
      "direct-session",
      "Project status?",
      undefined,
      "client-message-1",
    );
    expect(result.current.openInChat()).toBe(true);
    expect(h.setActiveSession).toHaveBeenCalledWith("direct-session");
    expect(h.setSelectedAgentId).toHaveBeenCalledWith("leader-1");
    expect(h.setSelectedProjectId).toHaveBeenCalledWith("project-1");
    expect(h.setOpen).toHaveBeenCalledWith(true);
  });

  it("waits for the leader session lookup before offering chat", async () => {
    h.sessions = [makeSession("leader-session", "leader-1", "project-1")];
    const { result } = renderConversation({
      projectId: "project-1",
      desk: makeDesk("leader-1", { isLeader: true }),
      leaderAgentId: "leader-1",
    });
    await waitFor(() => expect(result.current.status).toBe("loading"));
    expect(result.current.canSend).toBe(false);
    expect(api.sendChatMessage).not.toHaveBeenCalled();
  });

  it("creates a persistent direct leader chat when no direct session exists", async () => {
    h.sessions = [makeSession("leader-session", "leader-1", "project-1")];
    const { result } = renderConversation({
      projectId: "project-1",
      desk: makeDesk("leader-1", { isLeader: true }),
      leaderSessionId: "leader-session",
      leaderAgentId: "leader-1",
    });
    await waitFor(() => expect(result.current.status).toBe("ready"));

    await act(async () => {
      await result.current.send("Project status?");
    });

    expect(api.createChatSession).toHaveBeenCalledWith({
      agent_id: "leader-1",
      project_id: "project-1",
      title: "Project status?",
    });
    expect(api.sendChatMessage).toHaveBeenCalledWith(
      "session-new",
      "Project status?",
      undefined,
      "client-message-1",
    );
    expect(result.current.openInChat()).toBe(true);
    expect(h.setActiveSession).toHaveBeenCalledWith("session-new");
  });

  it("disables sending when the agent is forbidden", async () => {
    h.permissionAllowed = false;
    const { result } = renderConversation({ projectId: "project-1", desk: makeDesk() });
    await waitFor(() => expect(result.current.status).toBe("forbidden"));
    expect(result.current.canSend).toBe(false);
  });

  it("queues a message when the agent runtime is offline", async () => {
    h.availability = "offline";
    const { result } = renderConversation({ projectId: "project-1", desk: makeDesk() });
    await waitFor(() => expect(result.current.status).toBe("offline"));
    expect(result.current.canSend).toBe(true);

    await act(async () => {
      await result.current.send("Ping when online");
    });

    expect(api.createChatSession).toHaveBeenCalledWith({
      agent_id: "agent-1",
      project_id: "project-1",
      title: "Ping when online",
    });
    expect(api.sendChatMessage).toHaveBeenCalledWith(
      "session-new",
      "Ping when online",
      undefined,
      "client-message-1",
    );
  });

  it("marks an archived agent read-only", async () => {
    h.agents = [
      {
        id: "agent-1",
        runtime_id: "runtime-1",
        runtime_bound: true,
        archived_at: "2026-10-30T00:00:00.000Z",
      },
    ];
    const { result } = renderConversation({
      projectId: "project-1",
      desk: makeDesk("agent-1", {
        rawAgent: {
          id: "agent-1",
          runtime_id: "runtime-1",
          runtime_bound: true,
          archived_at: "2026-10-30T00:00:00.000Z",
        } as Agent,
      }),
    });
    await waitFor(() => expect(result.current.status).toBe("archived"));
    expect(result.current.canSend).toBe(false);
  });

  it("surfaces the server's queued state while keeping queue-capable agents sendable", async () => {
    h.sessions = [makeSession("session-queued", "agent-1", "project-1")];
    h.pending["session-queued"] = {
      task_id: "task-queued",
      status: "queued",
      supports_queue: true,
      queued_tasks: [],
    };
    const { result } = renderConversation({ projectId: "project-1", desk: makeDesk() });
    await waitFor(() => expect(result.current.status).toBe("queued"));
    expect(result.current.canSend).toBe(true);
  });

  it("does not keep a terminal pending task in the busy state", async () => {
    h.sessions = [makeSession("session-complete", "agent-1", "project-1")];
    h.pending["session-complete"] = {
      task_id: "task-complete",
      status: "completed",
      supports_queue: false,
      queued_tasks: [],
    };
    const { result } = renderConversation({ projectId: "project-1", desk: makeDesk() });

    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.canSend).toBe(true);
  });

  it("disables sending while a non-queueable task is still running", async () => {
    h.sessions = [makeSession("session-busy", "agent-1", "project-1")];
    h.pending["session-busy"] = {
      task_id: "task-running",
      status: "running",
      supports_queue: false,
      queued_tasks: [],
    };
    const { result } = renderConversation({ projectId: "project-1", desk: makeDesk() });

    await waitFor(() => expect(result.current.status).toBe("busy"));
    expect(result.current.canSend).toBe(false);
  });

  it("surfaces runtime-unavailable and server invocation-denied states", async () => {
    h.agents = [{ id: "agent-1", runtime_bound: false }];
    const offlineDesk = makeDesk("agent-1", {
      rawAgent: { id: "agent-1", runtime_bound: false } as Agent,
    });
    const runtime = renderConversation({ projectId: "project-1", desk: offlineDesk });
    await waitFor(() => expect(runtime.result.current.status).toBe("runtime_unavailable"));
    runtime.unmount();

    h.agents = [{ id: "agent-1", runtime_id: "runtime-1", runtime_bound: true }];
    vi.mocked(api.sendChatMessage).mockRejectedValueOnce({
      reasonCode: "invocation_not_allowed",
    });
    const denied = renderConversation({ projectId: "project-1", desk: makeDesk() });
    await waitFor(() => expect(denied.result.current.status).toBe("ready"));
    await act(async () => {
      await denied.result.current.send("Forbidden message");
    });

    expect(denied.result.current.status).toBe("forbidden");
    expect(denied.result.current.canSend).toBe(false);
    expect(denied.result.current.sendError?.kind).toBe("forbidden");
  });
});
