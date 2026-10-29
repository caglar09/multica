/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";
import { ApiClient } from "../api/client";
import { setApiInstance } from "../api";
import {
  ProjectPortabilityResultSchema,
  ProjectBundleSchema,
  ProjectContextPayloadSchema,
  ProjectExportResponseSchema,
} from "../api/schemas";
import type { PortableProjectBundle, PortableProjectExportResponse } from "../types";
import { setCurrentWorkspace } from "../platform/workspace-storage";
import { issueKeys } from "../issues/queries";
import { projectKeys } from "./queries";
import { useExportProject, useImportProject, useSyncProject } from "./portability";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const bundle: PortableProjectBundle = {
  schema_version: "multica.project.v1",
  exported_at: "2026-10-28T12:00:00Z",
  origin: { workspace_id: "origin-ws", project_id: "origin-project" },
  project: { title: "Portable project", status: "future_status" },
  tasks: [{ key: "TASK-1", title: "First task", status: "future_status" }],
};

const exportResponse: PortableProjectExportResponse = {
  bundle,
  tasks_markdown: "- [ ] First task",
  agent_guide_markdown: "Use TASKS.md as the task checklist.",
};

function response(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client: queryClient }, children);
  };
}

describe("project portability schemas", () => {
  it("accepts optional source path and daemon identity on discovery context", () => {
    expect(ProjectContextPayloadSchema.parse({
      root_name: "manual-project",
      files: [{ path: "src/main.ts", content: "export {}" }],
      local_path: "/Users/example/manual-project",
      daemon_id: "daemon-local",
    })).toMatchObject({
      local_path: "/Users/example/manual-project",
      daemon_id: "daemon-local",
    });
  });

  it("parses the portable bundle and export response while retaining unknown statuses", () => {
    expect(ProjectBundleSchema.parse(bundle)).toMatchObject({
      project: { status: "future_status" },
      tasks: [{ status: "future_status" }],
    });
    expect(ProjectExportResponseSchema.parse(exportResponse)).toMatchObject({
      tasks_markdown: "- [ ] First task",
      bundle: { schema_version: "multica.project.v1" },
    });
    expect(ProjectBundleSchema.parse({ ...bundle, tasks: null }).tasks).toEqual([]);
  });

  it("preserves sparse JSON upserts and explicit empty arrays", () => {
    const parsed = ProjectBundleSchema.parse({
      schema_version: "multica.project.v1",
      origin: {},
      project: { description: null },
      bootstrap: { plan: { goal: "partial", nodes: [] } },
      tasks: [
        { key: "TASK-1", status: "done" },
        { key: "TASK-2", labels: [], blocked_by_keys: [], comments: [], acceptance_criteria: [] },
      ],
    });

    expect(parsed.project).not.toHaveProperty("title");
    expect(parsed.project.description).toBeNull();
    expect(parsed.bootstrap?.plan).toMatchObject({ goal: "partial", nodes: [] });
    expect(parsed.bootstrap?.plan).not.toHaveProperty("requirements");
    expect(parsed.tasks[0]).not.toHaveProperty("title");
    expect(parsed.tasks[0]).not.toHaveProperty("labels");
    expect(parsed.tasks[1]).toMatchObject({
      labels: [],
      blocked_by_keys: [],
      comments: [],
      acceptance_criteria: [],
    });
  });

  it("validates the typed preview and result wire fields", () => {
    expect(ProjectPortabilityResultSchema.parse({
      project_id: "project-1",
      created_count: 1,
      updated_count: 0,
      unchanged_count: 2,
      preview: {
        project_action: "unchanged",
        project_changes: [],
        tasks: [{ key: "TASK-1", title: "First task", action: "create", changes: [] }],
        warnings: [],
      },
    })).toMatchObject({ project_id: "project-1", created_count: 1 });
    expect(ProjectPortabilityResultSchema.safeParse({
      project_id: "project-1",
      created_count: -1,
      updated_count: 0,
      unchanged_count: 0,
      warnings: [],
    }).success).toBe(false);
    expect(ProjectPortabilityResultSchema.parse({
      project_id: "project-1",
      created_count: 0,
      updated_count: 0,
      unchanged_count: 0,
      warnings: null,
    }).warnings).toEqual([]);
    expect(ProjectPortabilityResultSchema.parse({
      created_count: 0,
      updated_count: 0,
      unchanged_count: 0,
      preview: {
        project_action: "future_action",
        tasks: [{ key: "TASK-1", title: "First task", action: "future_action" }],
      },
    }).preview).toMatchObject({
      project_action: "unchanged",
      tasks: [{ action: "unchanged" }],
    });
    expect(ProjectPortabilityResultSchema.parse({
      created_count: 0,
      updated_count: 0,
      unchanged_count: 0,
      warnings: [],
    }).project_id).toBeUndefined();
  });

  it("parses discovery reports that contain empty or null optional lists", () => {
    const parsed = ProjectPortabilityResultSchema.parse({
      created_count: 2,
      updated_count: 0,
      unchanged_count: 0,
      warnings: [],
      discovery_report: {
        project: { title: "Manual project", purpose: "Purpose", goal: "Goal", architecture_summary: "Architecture" },
        boundaries: { in_scope: ["Feature"], out_of_scope: null, constraints: null, definition_of_done: null },
        implemented_features: [{ key: "feature-one", title: "Feature", description: "Already done", evidence: null }],
        next_tasks: [{ key: "task-one", title: "Next task", description: "Do next", stage: 1, blocked_by_keys: null, acceptance_criteria: null }],
      },
    });
    expect(parsed.discovery_report?.boundaries).toMatchObject({
      in_scope: ["Feature"],
      out_of_scope: [],
      constraints: [],
    });
    expect(parsed.discovery_report?.next_tasks[0]).toMatchObject({
      priority: "medium",
      blocked_by_keys: [],
      acceptance_criteria: [],
    });
  });
});

describe("project portability API client", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("calls export, import, and sync endpoints with the shared JSON contract", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(exportResponse))
      .mockResolvedValueOnce(response({
        project_id: "project-imported",
        created_count: 1,
        updated_count: 0,
        unchanged_count: 0,
        warnings: [],
      }))
      .mockResolvedValueOnce(response({
        project_id: "project-1",
        created_count: 0,
        updated_count: 1,
        unchanged_count: 0,
        warnings: [],
      }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.exportProject("project-1")).resolves.toMatchObject(exportResponse);
    await expect(client.importProject({
      markdown: "- [x] First task",
      project_title_hint: "Imported project",
      dry_run: true,
      create_missing_labels: false,
    })).resolves.toMatchObject({ project_id: "project-imported", created_count: 1 });
    await expect(client.syncProject("project-1", { bundle, dry_run: false })).resolves.toMatchObject({
      project_id: "project-1",
      updated_count: 1,
    });

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      "https://api.example.test/api/projects/project-1/export",
      "https://api.example.test/api/projects/import",
      "https://api.example.test/api/projects/project-1/sync",
    ]);
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({
      markdown: "- [x] First task",
      project_title_hint: "Imported project",
      dry_run: true,
      create_missing_labels: false,
    });
    expect(JSON.parse(String(fetchMock.mock.calls[2]?.[1]?.body))).toEqual({ bundle, dry_run: false });
  });

  it("returns null for malformed export and mutation responses", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ bundle: {}, tasks_markdown: 3 }))
      .mockResolvedValueOnce(response({ project_id: "project-1", created_count: -1 }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(client.exportProject("project-1")).resolves.toBeNull();
    await expect(client.importProject({ markdown: "- [ ] Task" })).resolves.toBeNull();
  });
});

describe("project portability hooks", () => {
  let queryClient: QueryClient;

  beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    setCurrentWorkspace("acme", "ws-1");
  });

  afterEach(() => {
    queryClient.clear();
    setCurrentWorkspace(null, null);
    vi.restoreAllMocks();
  });

  it("loads exports under workspace-scoped project cache keys", async () => {
    const exportProject = vi.fn().mockResolvedValue(exportResponse);
    setApiInstance({ exportProject } as unknown as ApiClient);
    const { result } = renderHook(() => useExportProject("project-1"), {
      wrapper: createWrapper(queryClient),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(exportProject).toHaveBeenCalledWith("project-1");
    expect(queryClient.getQueryData([
      ...projectKeys.detail("ws-1", "project-1"),
      "export",
    ])).toEqual(exportResponse);
  });

  it("skips cache invalidation for dry-run syncs and refreshes imported or synced data", async () => {
    const importResult = {
      project_id: "imported-project",
      created_count: 1,
      updated_count: 0,
      unchanged_count: 0,
      warnings: [],
    };
    const syncResult = {
      project_id: "project-1",
      created_count: 0,
      updated_count: 1,
      unchanged_count: 0,
      warnings: [],
    };
    const importProject = vi.fn().mockResolvedValue(importResult);
    const syncProject = vi.fn().mockResolvedValue(syncResult);
    setApiInstance({ importProject, syncProject } as unknown as ApiClient);

    const listKey = projectKeys.list("ws-1");
    const detailKey = projectKeys.detail("ws-1", "project-1");
    const issueKey = [...issueKeys.tableAll("ws-1"), "rows"];
    queryClient.setQueryData(listKey, { projects: [], total: 0 });
    queryClient.setQueryData(projectKeys.detail("ws-1", "imported-project"), { id: "imported-project" });
    queryClient.setQueryData(detailKey, { id: "project-1" });
    queryClient.setQueryData(issueKey, { rows: [] });

    const { result: syncHook } = renderHook(() => useSyncProject("project-1"), {
      wrapper: createWrapper(queryClient),
    });
    await act(async () => {
      await syncHook.current.mutateAsync({ markdown: "- [x] Existing task", dry_run: true });
    });
    expect(queryClient.getQueryState(issueKey)?.isInvalidated).toBe(false);

    await act(async () => {
      await syncHook.current.mutateAsync({ markdown: "- [x] Existing task" });
    });
    expect(queryClient.getQueryState(detailKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(issueKey)?.isInvalidated).toBe(true);

    queryClient.setQueryData(listKey, { projects: [], total: 0 });
    const { result: importHook } = renderHook(() => useImportProject(), {
      wrapper: createWrapper(queryClient),
    });
    await act(async () => {
      await importHook.current.mutateAsync({ markdown: "- [ ] New task" });
    });
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(projectKeys.detail("ws-1", "imported-project"))?.isInvalidated).toBe(true);
  });
});
