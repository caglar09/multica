"use client";

import { Copy, Download, RefreshCw, TriangleAlert } from "lucide-react";
import { useExportProject } from "@multica/core/projects";
import { Button } from "@multica/ui/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import { downloadBlob } from "../../editor/utils/mermaid-export";
import { useT } from "../../i18n/use-t";

interface ProjectExportDialogProps {
	projectId: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}

export function ProjectExportDialog({
	projectId,
	open,
	onOpenChange,
}: ProjectExportDialogProps) {
	const { t } = useT("projects");
	const { data, isLoading, isError, refetch } = useExportProject(projectId);

	const download = (filename: string, content: string, type: string) => {
		downloadBlob(new Blob([content], { type }), filename);
	};

	const copyCommand = async (command: string) => {
		const copied = await copyText(command);
		if (copied) toast.success(t(($) => $.portability.export.command_copied));
		else toast.error(t(($) => $.portability.export.copy_failed));
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
				<DialogHeader>
					<DialogTitle>{t(($) => $.portability.export.title)}</DialogTitle>
					<DialogDescription>
						{t(($) => $.portability.export.description)}
					</DialogDescription>
				</DialogHeader>

				{isLoading ? (
					<div className="flex items-center gap-2 py-4 text-caption text-muted-foreground" role="status">
						<RefreshCw className="size-3.5 animate-spin" aria-hidden="true" />
						{t(($) => $.portability.export.loading)}
					</div>
				) : isError || !data ? (
					<div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-caption text-destructive" role="alert">
						<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
						<span>{t(($) => $.portability.export.load_failed)}</span>
						<Button variant="outline" size="xs" className="ml-auto shrink-0" onClick={() => void refetch()}>
							{t(($) => $.portability.export.retry)}
						</Button>
					</div>
				) : (
					<div className="space-y-2">
						<Button className="w-full justify-start" onClick={() => download(
							"project.multica.json",
							JSON.stringify(data.bundle, null, 2),
							"application/json",
						)}>
							<Download aria-hidden="true" />
							{t(($) => $.portability.export.download_bundle)}
						</Button>
						<Button variant="outline" className="w-full justify-start" onClick={() => download(
							"TASKS.md",
							data.tasks_markdown,
							"text/markdown;charset=utf-8",
						)}>
							<Download aria-hidden="true" />
							{t(($) => $.portability.export.download_tasks)}
						</Button>
						<Button variant="outline" className="w-full justify-start" onClick={() => download(
							"AGENT_GUIDE.md",
							data.agent_guide_markdown,
							"text/markdown;charset=utf-8",
						)}>
							<Download aria-hidden="true" />
							{t(($) => $.portability.export.download_guide)}
						</Button>
						<div className="space-y-2 rounded-md bg-muted/50 p-3">
							<Button
								variant="outline"
								size="sm"
								onClick={() => void copyCommand(`multica project export ${projectId} --dir .`)}
							>
								<Copy aria-hidden="true" />
								{t(($) => $.portability.export.copy_export_command)}
							</Button>
							<Button
								variant="outline"
								size="sm"
								onClick={() => void copyCommand(`multica project sync ${projectId} --dir .`)}
							>
								<Copy aria-hidden="true" />
								{t(($) => $.portability.export.copy_sync_command)}
							</Button>
						</div>
					</div>
				)}

				<DialogFooter>
					<Button variant="ghost" onClick={() => onOpenChange(false)}>
						{t(($) => $.portability.import.cancel)}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
