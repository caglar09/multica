/** @vitest-environment jsdom */
import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useOfficeLiveData } from "./use-office-live-data";

const { mockTaskMessagesOptions, mockSnapshotOptions } = vi.hoisted(() => ({
  mockTaskMessagesOptions: vi.fn((taskId: string) => ({
    queryKey: ["office-task-messages", taskId],
    queryFn: async () => [{ task_id: taskId, issue_id: "issue-1", seq: 1, type: "text" as const, content: "Live" }],
  })),
  mockSnapshotOptions: vi.fn(() => ({
    queryKey: ["office-task-snapshot"],
    queryFn: async () => [
      {
        id: "11111111-1111-4111-8111-111111111111",
        agent_id: "agent-1",
        runtime_id: "runtime-1",
        issue_id: "issue-1",
        status: "running",
        priority: 1,
        dispatched_at: null,
        started_at: "2026-10-31T12:00:00.000Z",
        completed_at: null,
        result: null,
        error: null,
        created_at: "2026-10-31T12:00:00.000Z",
      },
      {
        id: "22222222-2222-4222-8222-222222222222",
        agent_id: "agent-1",
        runtime_id: "runtime-1",
        issue_id: "issue-2",
        status: "queued",
        priority: 1,
        dispatched_at: null,
        started_at: null,
        completed_at: null,
        result: null,
        error: null,
        created_at: "2026-10-31T12:01:00.000Z",
      },
    ],
  })),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/agents", () => ({
  agentTaskSnapshotOptions: mockSnapshotOptions,
  useWorkspacePresenceMap: () => ({ byAgent: new Map(), loading: false }),
}));
vi.mock("@multica/core/chat/queries", () => ({
  isTaskMessageTaskId: (taskId: string) => /^[0-9a-f-]{36}$/i.test(taskId),
  taskMessagesOptions: mockTaskMessagesOptions,
}));
vi.mock("@multica/core/issues", () => ({
  issueListOptions: () => ({ queryKey: ["office-issues"], queryFn: async () => [] }),
}));
vi.mock("@multica/core/projects", () => ({
  autonomousProjectOptions: () => ({ queryKey: ["office-autonomous"], queryFn: async () => null }),
  projectLeaderChangesOptions: () => ({ queryKey: ["office-changes"], queryFn: async () => ({ items: [] }) }),
}));
vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["office-agents"], queryFn: async () => [] }),
}));
vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeListOptions: () => ({ queryKey: ["office-runtimes"], queryFn: async () => [] }),
}));

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client: queryClient }, children);
  };
}

describe("useOfficeLiveData", () => {
  beforeEach(() => {
    mockTaskMessagesOptions.mockClear();
    mockSnapshotOptions.mockClear();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("reports loading, then subscribes only to active run messages", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { result } = renderHook(() => useOfficeLiveData(), { wrapper: wrapper(queryClient) });

    expect(result.current.isLoading).toBe(true);
    expect(result.current.isPartial).toBe(true);

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.isPartial).toBe(false);
    expect(result.current.scene.stats.runningTasks).toBe(1);
    expect(mockTaskMessagesOptions).toHaveBeenCalledWith("11111111-1111-4111-8111-111111111111");
    expect(mockTaskMessagesOptions).not.toHaveBeenCalledWith("22222222-2222-4222-8222-222222222222");
    queryClient.clear();
  });
});
