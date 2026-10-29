"use client";

import { useRef, useState } from "react";
import { FileUp, TriangleAlert } from "lucide-react";
import {
	useImportProject,
	useSyncProject,
} from "@multica/core/projects";
import { ProjectBundleSchema } from "@multica/core/api/schemas";
import type {
	ImportProjectRequest,
	ProjectContextPayload,
	ProjectDiscoveryReport,
	PortabilityResult,
	SyncProjectRequest,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "sonner";
import { useT } from "../../i18n/use-t";
import { collectProjectContext } from "./project-discovery-context";

type SourceFormat = "json" | "markdown";
type PortabilityRequest = ImportProjectRequest | SyncProjectRequest;

interface ProjectImportDialogProps {
	projectId?: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}

export function ProjectImportDialog({
	projectId,
	open,
	onOpenChange,
}: ProjectImportDialogProps) {
	const { t } = useT("projects");
	const fileInputRef = useRef<HTMLInputElement>(null);
	const directoryInputRef = useRef<HTMLInputElement>(null);
	const importProject = useImportProject();
	const syncProject = useSyncProject(projectId ?? "");
	const [format, setFormat] = useState<SourceFormat>("json");
	const [content, setContent] = useState("");
	const [filename, setFilename] = useState("");
	const [titleHint, setTitleHint] = useState("");
	const [projectContext, setProjectContext] = useState<ProjectContextPayload | null>(null);
	const [instruction, setInstruction] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [preview, setPreview] = useState<PortabilityResult | null>(null);
	const [previewRequest, setPreviewRequest] = useState<PortabilityRequest | null>(null);
	const pending = importProject.isPending || syncProject.isPending;
	const modeTitle = projectId
		? t(($) => $.portability.import.sync_title)
		: t(($) => $.portability.import.title);

	const clearPreview = () => {
		setPreview(null);
		setPreviewRequest(null);
		setError(null);
	};

	const buildRequest = (dryRun: boolean): PortabilityRequest => {
		if (projectContext && !projectId) {
			return {
				project_context: projectContext,
				instruction: instruction.trim(),
				dry_run: dryRun,
				create_missing_labels: true,
			};
		}
		if (!content.trim()) throw new Error(t(($) => $.portability.import.empty_input));
		if (format === "json") {
			let raw: unknown;
			try {
				raw = JSON.parse(content);
			} catch {
				throw new Error(t(($) => $.portability.import.invalid_json));
			}
			const parsed = ProjectBundleSchema.safeParse(raw);
			if (!parsed.success) throw new Error(t(($) => $.portability.import.invalid_json));
			return { bundle: parsed.data, dry_run: dryRun, create_missing_labels: true };
		}

		const request: SyncProjectRequest = {
			markdown: content,
			dry_run: dryRun,
			create_missing_labels: true,
		};
		if (!projectId && titleHint.trim()) {
			return { ...request, project_title_hint: titleHint.trim() };
		}
		return request;
	};

	const runRequest = async (request: PortabilityRequest) => {
		if (projectId) {
			const syncRequest: SyncProjectRequest = {
				bundle: request.bundle,
				markdown: request.markdown,
				dry_run: request.dry_run,
				create_missing_labels: request.create_missing_labels,
			};
			return syncProject.mutateAsync(syncRequest);
		}
		return importProject.mutateAsync(request as ImportProjectRequest);
	};

	const handleFile = async (file?: File) => {
		if (!file) return;
		clearPreview();
		setProjectContext(null);
		setFilename(file.name);
		setFormat(file.name.toLowerCase().endsWith(".md") ? "markdown" : "json");
		try {
			setContent(await file.text());
		} catch {
			setError(t(($) => $.portability.import.file_read_failed));
		}
	};

	const handleDirectory = async (files: FileList | null) => {
		if (!files?.length) return;
		clearPreview();
		setContent("");
		setFilename(t(($) => $.portability.import.selected_folder));
		try {
			setProjectContext(await collectProjectContext(files));
		} catch (cause) {
			setProjectContext(null);
			setError(cause instanceof Error ? cause.message : t(($) => $.portability.import.file_read_failed));
		}
	};

	const handlePreview = async () => {
		clearPreview();
		try {
			const request = buildRequest(true);
			setPreviewRequest(request);
			const result = await runRequest(request);
			if (!result) throw new Error(t(($) => $.portability.import.preview_failed));
			setPreview(result);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t(($) => $.portability.import.preview_failed));
		}
	};

	const handleApply = async () => {
		if (!preview || !previewRequest) return;
		setError(null);
		try {
			const request: PortabilityRequest = {
				...previewRequest,
				dry_run: false,
				...(!projectId && preview.discovery_report ? { discovery_report: preview.discovery_report } : {}),
			};
			const result = await runRequest(request);
			if (!result) throw new Error(t(($) => $.portability.import.apply_failed));
			toast.success(projectId
				? t(($) => $.portability.import.sync_complete)
				: t(($) => $.portability.import.import_complete));
			onOpenChange(false);
		} catch {
			setError(t(($) => $.portability.import.apply_failed));
		}
	};

	const openChange = (next: boolean) => {
		if (!pending) onOpenChange(next);
	};
	const warnings = [...new Set([
		...(preview?.preview?.warnings ?? []),
		...(preview?.warnings ?? []),
	])];

	return (
		<Dialog open={open} onOpenChange={openChange}>
			<DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-2xl">
				<DialogHeader>
					<DialogTitle>{modeTitle}</DialogTitle>
					<DialogDescription>
						{t(($) => $.portability.import.description)}
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4">
					<div className="space-y-2">
						<label htmlFor="project-portability-file" className="text-caption font-medium">
							{t(($) => $.portability.import.file_label)}
						</label>
						<div className="flex flex-wrap items-center gap-2">
							<input
								ref={fileInputRef}
								id="project-portability-file"
								type="file"
								accept=".json,.md,application/json,text/markdown"
								className="sr-only"
								disabled={pending}
								onChange={(event) => {
									const file = event.currentTarget.files?.[0];
									event.currentTarget.value = "";
									void handleFile(file);
								}}
							/>
							{!projectId ? (
								<input
									ref={directoryInputRef}
									id="project-portability-directory"
									type="file"
									multiple
									className="sr-only"
									disabled={pending}
									onChange={(event) => {
										const files = event.currentTarget.files;
										event.currentTarget.value = "";
										void handleDirectory(files);
									}}
								/>
							) : null}
							<Button
								variant="outline"
								type="button"
								disabled={pending}
								onClick={() => fileInputRef.current?.click()}
							>
								<FileUp aria-hidden="true" />
								{t(($) => $.portability.import.choose_file)}
							</Button>
							{!projectId ? (
								<Button
									variant="outline"
									type="button"
									disabled={pending}
									onClick={() => {
										directoryInputRef.current?.setAttribute("webkitdirectory", "");
										directoryInputRef.current?.click();
									}}
								>
									<FileUp aria-hidden="true" />
									{t(($) => $.portability.import.choose_folder)}
								</Button>
							) : null}
							{filename ? (
								<span className="max-w-full break-all text-caption text-muted-foreground">{filename}</span>
							) : null}
						</div>
					</div>

					{!projectContext ? <div className="space-y-2">
						<span className="text-caption font-medium">{t(($) => $.portability.import.format_label)}</span>
						<div className="flex gap-2">
							<Button
								type="button"
								variant={format === "json" ? "secondary" : "outline"}
								aria-pressed={format === "json"}
								disabled={pending}
								onClick={() => {
									setFormat("json");
									clearPreview();
								}}
							>
								{t(($) => $.portability.import.json_format)}
							</Button>
							<Button
								type="button"
								variant={format === "markdown" ? "secondary" : "outline"}
								aria-pressed={format === "markdown"}
								disabled={pending}
								onClick={() => {
									setFormat("markdown");
									clearPreview();
								}}
							>
								{t(($) => $.portability.import.markdown_format)}
							</Button>
						</div>
					</div> : null}

					{!projectContext ? <div className="space-y-2">
						<label htmlFor="project-portability-content" className="text-caption font-medium">
							{t(($) => $.portability.import.paste_label)}
						</label>
						<Textarea
							id="project-portability-content"
							value={content}
							rows={9}
							aria-label={t(($) => $.portability.import.paste_label)}
							placeholder={t(($) => $.portability.import.paste_placeholder)}
							disabled={pending}
							onChange={(event) => {
								setContent(event.target.value);
								setFilename("");
								clearPreview();
							}}
						/>
					</div> : null}

					{projectContext ? (
						<div className="space-y-2">
							<label htmlFor="project-discovery-instruction" className="text-caption font-medium">
								{t(($) => $.portability.discovery.instruction_label)}
							</label>
							<Textarea
								id="project-discovery-instruction"
								value={instruction}
								rows={3}
								placeholder={t(($) => $.portability.discovery.instruction_placeholder)}
								disabled={pending}
								onChange={(event) => {
									setInstruction(event.target.value);
									clearPreview();
								}}
							/>
							<p className="text-caption text-muted-foreground" role="note">
								{t(($) => $.portability.discovery.source_warning)}
							</p>
						</div>
					) : null}

					{!projectId && format === "markdown" ? (
						<div className="space-y-2">
							<label htmlFor="project-portability-title" className="text-caption font-medium">
								{t(($) => $.portability.import.title_hint_label)}
							</label>
							<Input
								id="project-portability-title"
								value={titleHint}
								placeholder={t(($) => $.portability.import.title_hint_placeholder)}
								disabled={pending}
								onChange={(event) => {
									setTitleHint(event.target.value);
									clearPreview();
								}}
							/>
						</div>
					) : null}

					{error ? (
						<div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-caption text-destructive" role="alert">
							<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
							<span>{error}</span>
						</div>
					) : null}

					{preview ? (
						<PreviewSummary preview={preview} warnings={warnings} report={preview.discovery_report ?? undefined} />
					) : null}
				</div>

				<DialogFooter>
					<Button variant="ghost" onClick={() => openChange(false)} disabled={pending}>
						{t(($) => $.portability.import.cancel)}
					</Button>
					{preview ? (
						<Button onClick={() => void handleApply()} disabled={pending} aria-busy={pending}>
							{projectId
								? t(($) => $.portability.import.apply_sync)
								: t(($) => $.portability.import.apply_import)}
						</Button>
					) : (
						<Button onClick={() => void handlePreview()} disabled={pending || (!projectContext && !content.trim())} aria-busy={pending}>
							{t(($) => $.portability.import.preview)}
						</Button>
					)}
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

function PreviewSummary({ preview, warnings, report }: { preview: PortabilityResult; warnings: string[]; report?: ProjectDiscoveryReport }) {
	const { t } = useT("projects");
	const tasks = preview.preview?.tasks ?? [];

	return (
		<div className="space-y-3 rounded-md border bg-muted/20 p-3" role="status" aria-label={t(($) => $.portability.preview.title)}>
			{report ? <DiscoveryReportSummary report={report} /> : null}
			<div className="flex flex-wrap gap-x-4 gap-y-1 text-caption">
				<span>{t(($) => $.portability.preview.created, { count: preview.created_count })}</span>
				<span>{t(($) => $.portability.preview.updated, { count: preview.updated_count })}</span>
				<span>{t(($) => $.portability.preview.unchanged, { count: preview.unchanged_count })}</span>
			</div>
			{tasks.length > 0 ? (
				<ul className="max-h-52 space-y-1 overflow-y-auto text-caption">
					{tasks.map((task) => (
						<li key={task.key} className="flex flex-wrap items-baseline gap-x-2">
							<span className="font-mono text-micro text-muted-foreground">{task.key}</span>
							<span className="font-medium">{task.title}</span>
							<span className="text-muted-foreground">{task.action}</span>
							{task.changes.length ? (
								<span className="text-muted-foreground">{task.changes.join(", ")}</span>
							) : null}
						</li>
					))}
				</ul>
			) : null}
			{warnings.length > 0 ? (
				<ul className="space-y-1 text-caption text-warning-foreground" aria-label={t(($) => $.portability.preview.warnings)}>
					{warnings.map((warning, index) => <li key={`${index}-${warning}`}>{warning}</li>)}
				</ul>
			) : null}
		</div>
	);
}

function DiscoveryReportSummary({ report }: { report: ProjectDiscoveryReport }) {
	const { t } = useT("projects");
	return (
		<div className="space-y-3">
			<div>
				<p className="text-caption font-semibold">{report.project.title}</p>
				<p className="text-caption text-muted-foreground">{report.project.purpose}</p>
				<p className="mt-1 text-caption">{report.project.goal}</p>
			</div>
			<div className="grid gap-3 sm:grid-cols-2">
				<div>
					<p className="text-micro font-semibold text-muted-foreground">{t(($) => $.portability.discovery.boundaries)}</p>
					<ul className="mt-1 space-y-1 text-caption">
						{[...report.boundaries.constraints, ...report.boundaries.out_of_scope].map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}
					</ul>
				</div>
				<div>
					<p className="text-micro font-semibold text-muted-foreground">{t(($) => $.portability.discovery.implemented)}</p>
					<ul className="mt-1 space-y-1 text-caption">
						{report.implemented_features.map((feature) => <li key={feature.key}>{feature.title}</li>)}
					</ul>
				</div>
				<div className="sm:col-span-2">
					<p className="text-micro font-semibold text-muted-foreground">{t(($) => $.portability.discovery.next_tasks)}</p>
					<ul className="mt-1 space-y-1 text-caption">
						{report.next_tasks.map((task) => (
							<li key={task.key}>
								<span className="font-medium">{task.title}</span>
								{task.blocked_by_keys.length > 0 ? <span className="text-muted-foreground"> — {t(($) => $.portability.discovery.blocked_by)}: {task.blocked_by_keys.join(", ")}</span> : null}
							</li>
						))}
					</ul>
				</div>
			</div>
		</div>
	);
}
