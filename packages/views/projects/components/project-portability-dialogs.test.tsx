import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { renderWithI18n } from "../../test/i18n";
import { ProjectExportDialog } from "./project-export-dialog";
import { ProjectImportDialog } from "./project-import-dialog";

const mocks = vi.hoisted(() => ({
	importProject: vi.fn(),
	syncProject: vi.fn(),
	refetch: vi.fn(),
	downloadBlob: vi.fn(),
	localDaemon: { running: false, daemonId: null as string | null },
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@multica/core/runtimes", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@multica/core/runtimes")>();
	return {
		...actual,
		runtimeListOptions: (wsId: string) => ({
			queryKey: ["test-runtimes", wsId],
			queryFn: async () => [
				{ id: "runtime-codex", workspace_id: "ws-1", daemon_id: "daemon-local", name: "Codex (my-mac)", runtime_mode: "local", provider: "codex", launch_header: "", status: "online", device_info: "my-mac", metadata: {}, owner_id: "user-1", visibility: "private", last_seen_at: "2026-10-29T00:00:00Z", created_at: "2026-10-29T00:00:00Z", updated_at: "2026-10-29T00:00:00Z" },
				{ id: "runtime-antigravity", workspace_id: "ws-1", daemon_id: "daemon-local", name: "Antigravity (my-mac)", runtime_mode: "local", provider: "antigravity", launch_header: "", status: "online", device_info: "my-mac", metadata: {}, owner_id: "user-1", visibility: "private", last_seen_at: "2026-10-29T00:00:00Z", created_at: "2026-10-29T00:00:00Z", updated_at: "2026-10-29T00:00:00Z" },
			],
		}),
		runtimeModelsOptions: (runtimeId: string | null) => ({
			queryKey: ["test-runtime-models", runtimeId],
			queryFn: async () => ({
				supported: true,
				models: [{ id: "gemini-3.1-pro", label: "Gemini 3.1 Pro" }],
			}),
		}),
	};
});

vi.mock("../../platform", () => ({ useLocalDaemonStatus: () => mocks.localDaemon }));

vi.mock("@multica/core/projects", () => ({
	useExportProject: () => ({
		data: {
			bundle: {
				schema_version: "multica.project.v1",
				origin: { project_id: "project-1" },
				project: { title: "Portable project" },
				tasks: [],
			},
			tasks_markdown: "# Portable project",
			agent_guide_markdown: "Work from TASKS.md",
		},
		isLoading: false,
		isError: false,
		refetch: mocks.refetch,
	}),
	useImportProject: () => ({
		mutateAsync: mocks.importProject,
		isPending: false,
	}),
	useSyncProject: () => ({
		mutateAsync: mocks.syncProject,
		isPending: false,
	}),
}));

vi.mock("../../editor/utils/mermaid-export", () => ({
	downloadBlob: mocks.downloadBlob,
}));

vi.mock("@multica/ui/lib/clipboard", () => ({ copyText: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function renderWithQueryClient(ui: ReactElement) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return renderWithI18n(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

describe("project portability dialogs", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.localDaemon = { running: false, daemonId: null };
		mocks.importProject.mockResolvedValue({
			project_id: "project-2",
			created_count: 1,
			updated_count: 0,
			unchanged_count: 0,
			warnings: [],
			preview: {
				project_action: "create",
				project_changes: [],
				tasks: [{ key: "TASK-1", title: "First task", action: "create", changes: ["title"] }],
				warnings: [],
			},
		});
		mocks.syncProject.mockResolvedValue({
			project_id: "project-1",
			created_count: 1,
			updated_count: 0,
			unchanged_count: 0,
			warnings: [],
		});
	});

	it("previews and applies a JSON import with the same bundle", async () => {
		const user = userEvent.setup();
		const bundle = {
			schema_version: "multica.project.v1",
			origin: {},
			project: { title: "Portable project" },
			resources: [],
			tasks: [],
		};
		const onOpenChange = vi.fn();
		renderWithQueryClient(<ProjectImportDialog open onOpenChange={onOpenChange} />);

		fireEvent.change(screen.getByRole("textbox", { name: "Or paste content" }), {
			target: { value: JSON.stringify(bundle) },
		});
		await user.click(screen.getByRole("button", { name: "Preview changes" }));

		await waitFor(() => expect(mocks.importProject).toHaveBeenCalledWith({
			bundle,
			dry_run: true,
			create_missing_labels: true,
		}));
		expect(await screen.findByText("TASK-1")).toBeTruthy();
		await user.click(screen.getByRole("button", { name: "Import project" }));
		await waitFor(() => expect(mocks.importProject).toHaveBeenNthCalledWith(2, {
			bundle,
			dry_run: false,
			create_missing_labels: true,
		}));
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("syncs Markdown through the sync endpoint and keeps sync additive", async () => {
		const user = userEvent.setup();
		const onOpenChange = vi.fn();
		renderWithQueryClient(<ProjectImportDialog projectId="project-1" open onOpenChange={onOpenChange} />);
		await user.click(screen.getByRole("button", { name: "Markdown task list" }));
		const markdown = "# Portable project\n- [x] First task";
		fireEvent.change(screen.getByRole("textbox", { name: "Or paste content" }), {
			target: { value: markdown },
		});
		await user.click(screen.getByRole("button", { name: "Preview changes" }));

		await waitFor(() => expect(mocks.syncProject).toHaveBeenCalledWith({
			markdown,
			dry_run: true,
			create_missing_labels: true,
		}));
		await user.click(screen.getByRole("button", { name: "Apply sync" }));
		await waitFor(() => expect(mocks.syncProject).toHaveBeenNthCalledWith(2, {
			markdown,
			dry_run: false,
			create_missing_labels: true,
		}));
		expect(mocks.importProject).not.toHaveBeenCalled();
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("previews a selected project folder and applies only after review", async () => {
		const user = userEvent.setup();
		const onOpenChange = vi.fn();
		const report = {
			project: { title: "Manual project", purpose: "Track inventory", goal: "Reduce stock errors", architecture_summary: "A small API." },
			boundaries: { in_scope: [], out_of_scope: ["Shipping"], constraints: ["Keep the API"], definition_of_done: [] },
			implemented_features: [{ key: "feature-auth", title: "Authentication", description: "Users can sign in.", evidence: ["src/auth.any"] }],
			next_tasks: [{ key: "task-audit", title: "Add an audit trail", description: "Record changes.", priority: "high", stage: 1, parent_key: "", blocked_by_keys: ["feature-auth"], acceptance_criteria: ["Changes show an actor."] }],
		};
		mocks.importProject
			.mockResolvedValueOnce({
				project_id: null,
				created_count: 2,
				updated_count: 0,
				unchanged_count: 0,
				warnings: ["Attach a source before agents edit files."],
				discovery_report: report,
				preview: { project_action: "create", project_changes: [], tasks: [], warnings: [] },
			})
			.mockResolvedValueOnce({ project_id: "project-2", created_count: 2, updated_count: 0, unchanged_count: 0, warnings: [] });
		renderWithQueryClient(<ProjectImportDialog open onOpenChange={onOpenChange} />);

		await user.click(screen.getByRole("button", { name: "Choose project folder" }));
		const file = {
			name: "main.any",
			size: 6,
			webkitRelativePath: "manual-project/src/main.any",
			arrayBuffer: async () => new TextEncoder().encode("source").buffer,
		};
		fireEvent.change(document.getElementById("project-portability-directory")!, { target: { files: [file] } });
		await user.click(screen.getByRole("button", { name: "Preview changes" }));

		await waitFor(() => {
			expect(mocks.importProject).toHaveBeenCalledWith(expect.objectContaining({
				project_context: { root_name: "manual-project", files: [{ path: "src/main.any", content: "source" }], warnings: [] },
				dry_run: true,
			}));
		});
		expect(await screen.findByText("Add an audit trail")).toBeTruthy();
		expect(screen.getByText("Authentication")).toBeTruthy();
		expect(screen.getByText(/Agents cannot edit them/)).toBeTruthy();
		await user.click(screen.getByRole("button", { name: "Import project" }));
		await waitFor(() => expect(mocks.importProject).toHaveBeenNthCalledWith(2, expect.objectContaining({
			project_context: expect.any(Object),
			discovery_report: report,
			dry_run: false,
		})));
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("uses the selected connected runtime and model while preserving the Mika default", async () => {
		const user = userEvent.setup();
		mocks.localDaemon = { running: true, daemonId: "daemon-local" };
		mocks.importProject.mockResolvedValueOnce({
			project_id: null,
			created_count: 0,
			updated_count: 0,
			unchanged_count: 0,
			warnings: [],
			discovery_report: {
				project: { title: "X", purpose: "X", goal: "X", architecture_summary: "X" },
				boundaries: { in_scope: [], out_of_scope: [], constraints: [], definition_of_done: [] },
				implemented_features: [],
				next_tasks: [],
			},
			preview: { project_action: "create", project_changes: [], tasks: [], warnings: [] },
		});
		renderWithQueryClient(<ProjectImportDialog open onOpenChange={vi.fn()} />);
		await user.click(screen.getByRole("button", { name: "Choose project folder" }));
		const file = {
			name: "main.any",
			size: 6,
			webkitRelativePath: "manual-project/src/main.any",
			path: "/Users/example/manual-project/src/main.any",
			arrayBuffer: async () => new TextEncoder().encode("source").buffer,
		};
		fireEvent.change(document.getElementById("project-portability-directory")!, { target: { files: [file] } });
		await screen.findByRole("combobox", { name: "Analysis runtime" });
		await user.click(screen.getByRole("combobox", { name: "Analysis runtime" }));
		await user.click(screen.getByRole("option", { name: "Antigravity" }));
		await user.click(screen.getByRole("combobox", { name: "Model" }));
		await user.click(await screen.findByRole("option", { name: "Gemini 3.1 Pro" }));
		await user.click(screen.getByRole("button", { name: "Preview changes" }));

		await waitFor(() => expect(mocks.importProject).toHaveBeenCalledWith(expect.objectContaining({
			project_context: expect.objectContaining({
				local_path: "/Users/example/manual-project",
				daemon_id: "daemon-local",
			}),
			runtime_id: "runtime-antigravity",
			model: "gemini-3.1-pro",
			dry_run: true,
		})));
	});

	it("keeps the selected folder files when resetting the directory input", async () => {
		const user = userEvent.setup();
		const onOpenChange = vi.fn();
		renderWithQueryClient(<ProjectImportDialog open onOpenChange={onOpenChange} />);
		await user.click(screen.getByRole("button", { name: "Choose project folder" }));

		const file = {
			name: "main.any",
			size: 6,
			webkitRelativePath: "manual-project/src/main.any",
			arrayBuffer: async () => new TextEncoder().encode("source").buffer,
		};
		const input = document.getElementById("project-portability-directory")!;
		let selectedFiles = [file];
		const liveFileList = {
			get length() { return selectedFiles.length; },
			item: (index: number) => selectedFiles[index] ?? null,
			*[Symbol.iterator]() { yield* selectedFiles; },
		};
		Object.defineProperty(input, "files", { configurable: true, get: () => liveFileList });
		Object.defineProperty(input, "value", {
			configurable: true,
			get: () => selectedFiles.length ? "C:\\fakepath\\manual-project" : "",
			set: (value: string) => { if (!value) selectedFiles = []; },
		});
		fireEvent.change(input);
		await user.click(screen.getByRole("button", { name: "Preview changes" }));

		await waitFor(() => expect(mocks.importProject).toHaveBeenCalledWith(expect.objectContaining({
			project_context: { root_name: "manual-project", files: [{ path: "src/main.any", content: "source" }], warnings: [] },
			dry_run: true,
		})));
	});

	it("downloads the portable bundle", async () => {
		const user = userEvent.setup();
		renderWithI18n(
			<ProjectExportDialog projectId="project-1" open onOpenChange={vi.fn()} />,
		);
		await user.click(screen.getByRole("button", { name: "Download project bundle" }));
		expect(mocks.downloadBlob).toHaveBeenCalledWith(
			expect.any(Blob),
			"project.multica.json",
		);
	});
});
