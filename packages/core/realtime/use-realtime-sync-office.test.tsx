/** @vitest-environment jsdom */
import { QueryClient, QueryClientProvider, QueryObserver } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { chatKeys, taskMessagesOptions } from "../chat/queries";
import type { TaskMessagePayload } from "../types/events";
import type { WSClient } from "../api/ws-client";
import { autonomousProjectKeys } from "../projects/autonomous";
import { useRealtimeSync, type RealtimeSyncStores } from "./use-realtime-sync";

vi.mock("../platform/workspace-storage", () => ({
  getCurrentWsId: () => "ws-1",
  getCurrentSlug: () => "test-ws",
  createWorkspaceAwareStorage: (adapter: unknown) => adapter,
  registerForWorkspaceRehydration: () => {},
}));
vi.mock("../paths", () => ({
  useHasOnboarded: () => true,
  resolvePostAuthDestination: () => "/",
}));

const TASK_ID = "11111111-1111-4111-8111-111111111111";
type HandlerMap = Map<string, (payload: unknown) => void>;

function createMockWs(handlers: HandlerMap): WSClient {
  return {
    on: vi.fn((event: string, handler: (payload: unknown) => void) => {
      handlers.set(event, handler);
      return () => handlers.delete(event);
    }),
    onAny: vi.fn(() => () => {}),
    onReconnect: vi.fn(() => () => {}),
  } as unknown as WSClient;
}

function stores(): RealtimeSyncStores {
  return {
    authStore: Object.assign(() => ({}), {
      getState: () => ({ user: { id: "member-1" } }),
      subscribe: () => () => {},
      setState: () => {},
      destroy: () => {},
    }),
  } as unknown as RealtimeSyncStores;
}

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

function taskMessage(seq: number): TaskMessagePayload {
  return { task_id: TASK_ID, issue_id: "issue-1", seq, type: "text", content: `frame ${seq}` };
}

describe("useRealtimeSync office refresh signals", () => {
  let queryClient: QueryClient;
  let handlers: HandlerMap;
  let listTaskMessages: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    handlers = new Map();
    listTaskMessages = vi.fn(async () => [] as TaskMessagePayload[]);
    setApiInstance({ listTaskMessages } as unknown as ApiClient);
  });

  afterEach(() => {
    queryClient.clear();
    setApiInstance(undefined as unknown as ApiClient);
  });

  function mount() {
    renderHook(() => useRealtimeSync(createMockWs(handlers), stores()), {
      wrapper: wrapper(queryClient),
    });
  }

  it("invalidates the project's autonomous snapshot when an issue changes", () => {
    mount();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");
    handlers.get("issue:updated")?.({ issue: { id: "issue-1", status: "blocked" } });

    expect(invalidate).toHaveBeenCalledWith({ queryKey: autonomousProjectKeys.all("ws-1") });
  });

  it("writes task messages to the mounted run timeline and only marks snapshots stale", async () => {
    mount();
    const observer = new QueryObserver(queryClient, taskMessagesOptions(TASK_ID));
    const release = observer.subscribe(() => {});
    await waitFor(() => expect(listTaskMessages).toHaveBeenCalled());
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    handlers.get("task:message")?.(taskMessage(1));

    expect(queryClient.getQueryData(chatKeys.taskMessages(TASK_ID))).toEqual([taskMessage(1)]);
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: autonomousProjectKeys.all("ws-1"),
      refetchType: "none",
    });
    release();
  });
});
