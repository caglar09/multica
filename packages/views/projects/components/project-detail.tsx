"use client";

import { useMemo, useState, useCallback, useRef, useEffect } from "react";
import { useDefaultLayout, usePanelRef } from "react-resizable-panels";
import {
	BarChart3,
	Check,
	ChevronRight,
	Copy,
	LayoutDashboard,
	Link2,
	ListTodo,
	MoreHorizontal,
	PanelRight,
	Pin,
	PinOff,
	SlidersHorizontal,
	Sparkles,
	Terminal,
	Trash2,
	UserMinus,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import type { ProjectStatus, ProjectPriority } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { projectDetailOptions } from "@multica/core/projects/queries";
import {
	autonomousProjectOptions,
	projectLeaderChangesOptions,
	projectLeaderChatOptions,
	useApproveProjectLeaderChange,
	useRejectProjectLeaderChange,
	useResolveAutonomousEscalation,
	useConfirmAutonomousTeam,
} from "@multica/core/projects";
import type { AutonomousRoleRuntimeAssignment } from "@multica/core/types";
import {
	useUpdateProject,
	useDeleteProject,
} from "@multica/core/projects/mutations";
import { pinListOptions } from "@multica/core/pins";
import { useCreatePin, useDeletePin } from "@multica/core/pins";
import {
	memberListOptions,
	agentListOptions,
} from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssuesScope } from "@multica/core/issues/stores";
import { useChatStore, useRecentContextStore } from "@multica/core/chat";
import { useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import {
	PROJECT_STATUS_ORDER,
	PROJECT_STATUS_CONFIG,
	PROJECT_PRIORITY_ORDER,
} from "@multica/core/projects/config";
import { getProjectIssueMetrics } from "./project-issue-metrics";
import { ActorAvatar } from "../../common/actor-avatar";
import { currentPath, useNavigation } from "../../navigation";
import {
	TitleEditor,
	ContentEditor,
	type ContentEditorRef,
} from "../../editor";
import { PriorityIcon } from "../../issues/components/priority-icon";
import { ProjectResourcesSection } from "./project-resources-section";
import { ProjectStartDatePicker } from "./project-start-date-picker";
import { ProjectDueDatePicker } from "./project-due-date-picker";
import { AutonomousControlCenter } from "./autonomous-control-center";
import { ProjectReport } from "./project-report";
import { ProjectCockpitView } from "./cockpit";
import { IssueSurface } from "../../issues/surface/issue-surface";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import {
	ResizablePanelGroup,
	ResizablePanel,
	ResizableHandle,
} from "@multica/ui/components/ui/resizable";
import { Sheet, SheetContent } from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
	Popover,
	PopoverTrigger,
	PopoverContent,
} from "@multica/ui/components/ui/popover";
import {
	Tooltip,
	TooltipTrigger,
	TooltipContent,
} from "@multica/ui/components/ui/tooltip";
import { EmojiPicker } from "@multica/ui/components/common/emoji-picker";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import {
	AnimatedRightSidebar,
	getAnimatedRightSidebarInitialOpen,
	rightSidebarPanelMotionProps,
	useAnimatedRightSidebarState,
	useRightSidebarShortcut,
} from "../../layout/animated-right-sidebar";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../../i18n";
import { useProjectStatusLabels, useProjectPriorityLabels } from "./labels";
import { matchesPinyin } from "../../editor/extensions/pinyin-match";
import { openLocalDirectory } from "../../platform/local-directory";
import { useLocalDaemonStatus } from "../../platform/use-local-daemon-status";

// ---------------------------------------------------------------------------
// Property row — sidebar property display
// ---------------------------------------------------------------------------

function PropRow({
	label,
	children,
}: {
	label: string;
	children: React.ReactNode;
}) {
	return (
		<div className="flex min-h-8 items-center justify-between gap-2 rounded-md px-2 py-0.5 hover:bg-accent/40 transition-colors">
			<span className="shrink-0 text-caption text-muted-foreground">
				{label}
			</span>
			<div className="flex min-w-0 items-center justify-end gap-1.5 text-caption truncate">
				{children}
			</div>
		</div>
	);
}

// ---------------------------------------------------------------------------
// ProjectDetail
// ---------------------------------------------------------------------------

export function ProjectDetail({ projectId }: { projectId: string }) {
	const { t } = useT("projects");
	const statusLabels = useProjectStatusLabels();
	const priorityLabels = useProjectPriorityLabels();
	const wsId = useWorkspaceId();
	const wsPaths = useWorkspacePaths();
	const router = useNavigation();
	const userId = useAuthStore((s) => s.user?.id);
	const { data: project, isLoading } = useQuery(
		projectDetailOptions(wsId, projectId),
	);
	const { data: autonomousSnapshot } = useQuery(
		autonomousProjectOptions(wsId, projectId),
	);
	const localDaemon = useLocalDaemonStatus();
	const recordRecentContext = useRecentContextStore((s) => s.recordVisit);
	useEffect(() => {
		if (project) {
			recordRecentContext(wsId, {
				type: "project",
				id: project.id,
				label: project.title,
				subtitle: project.description ?? undefined,
				icon: project.icon,
				projectStatus: project.status,
			});
		}
	}, [
		project?.id,
		project?.title,
		project?.description,
		project?.icon,
		project?.status,
		recordRecentContext,
		wsId,
	]);
	const issueTab = useIssuesScope(`project:${projectId}`);
	const issueScope = useMemo(
		() => ({ type: "project" as const, projectId, actorKind: issueTab }),
		[projectId, issueTab],
	);
	const { data: members = [] } = useQuery(memberListOptions(wsId));
	const { data: agents = [] } = useQuery(agentListOptions(wsId));
	const { getActorName } = useActorName();
	const updateProject = useUpdateProject();
	const deleteProject = useDeleteProject();
	const { data: pinnedItems = [] } = useQuery({
		...pinListOptions(wsId, userId ?? ""),
		enabled: !!userId,
	});
	const isPinned = pinnedItems.some(
		(p) => p.item_type === "project" && p.item_id === projectId,
	);
	const isWorkspaceAdmin = useMemo(() => {
		if (!userId) return false;
		const me = members.find((m) => m.user_id === userId);
		return me?.role === "owner" || me?.role === "admin";
	}, [members, userId]);
	const createPin = useCreatePin();
	const deletePinMut = useDeletePin();
	const descEditorRef = useRef<ContentEditorRef>(null);
	const isMobile = useIsMobile();
	const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
	const [iconPickerOpen, setIconPickerOpen] = useState(false);
	const localProjectDirectory = useMemo(() => {
		const directories = project?.working_directories ?? [];
		if (directories.length === 0) return undefined;
		if (localDaemon.daemonId) {
			const onThisMachine = directories.find(
				(directory) => directory.daemon_id === localDaemon.daemonId,
			);
			if (onThisMachine) return onThisMachine;
		}
		// Browser/self-host mode may not have resolved the local daemon yet. The
		// backend returns newest-per-daemon order, so keep the folder visible
		// immediately and let the loopback daemon validate/open it on click.
		return directories[0];
	}, [project, localDaemon.daemonId]);

	const handleOpenProjectDirectory = useCallback(async () => {
		if (!localProjectDirectory) return;
		const result = await openLocalDirectory(localProjectDirectory.path, {
			daemonId: localProjectDirectory.daemon_id,
			healthPort: localProjectDirectory.health_port,
		});
		if (!result.ok) {
			toast.error(
				result.error ?? t(($) => $.resources.toast_local_open_failed),
			);
		}
	}, [localProjectDirectory, t]);
	const leaderChat = useQuery({
		...projectLeaderChatOptions(wsId, projectId),
		enabled: Boolean(autonomousSnapshot?.enabled),
	});
	const leaderChanges = useQuery({
		...projectLeaderChangesOptions(wsId, projectId),
		enabled: Boolean(autonomousSnapshot?.enabled),
	});
	const approveLeaderChange = useApproveProjectLeaderChange();
	const rejectLeaderChange = useRejectProjectLeaderChange();
	const resolveEscalation = useResolveAutonomousEscalation();
	const confirmTeam = useConfirmAutonomousTeam();

	const handleConfirmTeam = useCallback(
		(assignments: AutonomousRoleRuntimeAssignment[]) => {
			confirmTeam.mutate(
				{ projectId, assignments },
				{
					onSuccess: () =>
						toast.success(t(($) => $.cockpit.toast_team_confirmed)),
					onError: () =>
						toast.error(t(($) => $.cockpit.toast_team_confirm_failed)),
				},
			);
		},
		[confirmTeam, projectId, t],
	);

	const handleOpenLeaderChat = useCallback(async () => {
		let sessionId = leaderChat.data?.session?.id;
		let pmAgentId = leaderChat.data?.leader?.id;

		// If leader chat query hasn't resolved yet, try fetching fresh from API
		if (!sessionId && autonomousSnapshot?.enabled) {
			try {
				const freshLeaderChat = await api.getProjectLeaderChat(projectId);
				if (freshLeaderChat?.session?.id) {
					sessionId = freshLeaderChat.session.id;
					pmAgentId = freshLeaderChat.leader?.id;
				}
			} catch {
				// The dedicated session is required for the protected PM flow.
			}
		}

		if (!sessionId) {
			toast.error(t(($) => $.cockpit.toast_leader_chat_unavailable));
			return;
		}

		// Synchronize ChatStore:
		useChatStore.getState().setActiveSession(sessionId);

		if (pmAgentId) {
			useChatStore.getState().setSelectedAgentId(pmAgentId);
		}
		useChatStore.getState().setSelectedProjectId(projectId);
		useChatStore.getState().setOpen(true);
	}, [
		leaderChat.data,
		autonomousSnapshot?.enabled,
		t,
		projectId,
	]);

	const [propertiesOpen, setPropertiesOpen] = useState(true);
	const [progressOpen, setProgressOpen] = useState(true);
	const [descriptionOpen, setDescriptionOpen] = useState(true);
	const tabParam = router.searchParams.get("tab");
	const contentView: "cockpit" | "issues" | "autonomous" | "report" =
		tabParam === "issues"
			? "issues"
			: tabParam === "autonomous"
				? "autonomous"
				: tabParam === "report"
					? "report"
					: "cockpit";

	const handleContentViewChange = useCallback(
		(view: "cockpit" | "issues" | "autonomous" | "report") => {
			const params = new URLSearchParams(router.searchParams);
			if (view === "cockpit") {
				params.delete("tab");
			} else {
				params.set("tab", view);
			}

			const search = params.toString();
			const nextPath = `${router.pathname}${search ? `?${search}` : ""}${router.hash}`;
			if (nextPath !== currentPath(router)) {
				router.replace(nextPath);
			}
		},
		[router],
	);

	// Sidebar panel
	const { defaultLayout, onLayoutChanged } = useDefaultLayout({
		id: "multica_project_detail_layout",
	});
	const sidebarRef = usePanelRef();
	const rightSidebarShortcutTargetRef = useRef<HTMLDivElement | null>(null);
	const desktopSidebarInitialOpen = getAnimatedRightSidebarInitialOpen(
		true,
		defaultLayout,
	);
	// Desktop and mobile sidebar state must be separate. A single state defaulting
	// to `true` made the mobile <Sheet> mount in the open position on first render
	// (after `useIsMobile()` flipped from false→true), briefly covering the page
	// with its modal backdrop and locking scroll — leaving the page unresponsive.
	const {
		open: desktopSidebarOpen,
		visualOpen: desktopSidebarVisualOpen,
		motionEnabled: desktopSidebarMotionEnabled,
		beginToggle: beginDesktopSidebarToggle,
		handleResize: handleDesktopSidebarResize,
	} = useAnimatedRightSidebarState(desktopSidebarInitialOpen);
	const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
	const sidebarOpen = isMobile ? mobileSidebarOpen : desktopSidebarOpen;

	useEffect(() => {
		if (isMobile) {
			setMobileSidebarOpen(false);
		}
	}, [isMobile]);

	const handleToggleSidebar = useCallback(() => {
		if (isMobile) {
			setMobileSidebarOpen((open) => !open);
			return;
		}

		const panel = sidebarRef.current;
		if (!panel) return;
		const nextOpen = panel.isCollapsed();
		beginDesktopSidebarToggle(nextOpen);
		window.requestAnimationFrame(() => {
			if (nextOpen) panel.expand();
			else panel.collapse();
		});
	}, [beginDesktopSidebarToggle, isMobile, sidebarRef]);

	useRightSidebarShortcut(rightSidebarShortcutTargetRef, handleToggleSidebar);

	// Lead popover
	const [leadOpen, setLeadOpen] = useState(false);
	const [leadFilter, setLeadFilter] = useState("");
	const leadQuery = leadFilter.toLowerCase();
	const filteredMembers = members.filter(
		(m) =>
			m.name.toLowerCase().includes(leadQuery) ||
			matchesPinyin(m.name, leadQuery),
	);
	const filteredAgents = agents.filter(
		(a) =>
			!a.archived_at &&
			(a.name.toLowerCase().includes(leadQuery) ||
				matchesPinyin(a.name, leadQuery)),
	);

	const handleUpdateField = useCallback(
		(
			data: Parameters<typeof updateProject.mutate>[0] extends {
				id: string;
			} & infer R
				? R
				: never,
		) => {
			if (!project) return;
			updateProject.mutate({ id: project.id, ...data });
		},
		[project, updateProject],
	);

	const handleDelete = useCallback(() => {
		if (!project) return;
		deleteProject.mutate(project.id, {
			onSuccess: () => {
				toast.success(t(($) => $.detail.toast_project_deleted));
				router.push(wsPaths.projects());
			},
		});
	}, [project, deleteProject, router, wsPaths, t]);

	if (isLoading) {
		return (
			<div className="mx-auto w-full max-w-4xl px-8 py-10 space-y-4">
				<Skeleton className="h-5 w-32" />
				<Skeleton className="h-8 w-64" />
				<Skeleton className="h-4 w-96" />
				<Skeleton className="h-40 w-full mt-8" />
			</div>
		);
	}

	if (!project) {
		return (
			<div className="flex items-center justify-center h-full text-muted-foreground">
				{t(($) => $.detail.not_found)}
			</div>
		);
	}

	const issueMetrics = getProjectIssueMetrics(project);
	const statusCfg = PROJECT_STATUS_CONFIG[project.status];

	const sidebarContent = (
		<div className="flex h-full flex-col select-none overflow-hidden bg-background">
			{/* 1. Header */}
			<div className="flex h-12 shrink-0 items-center justify-between border-b px-4 bg-muted/20">
				<div className="flex items-center gap-2">
					<SlidersHorizontal className="size-4 text-primary" />
					<span className="text-xs font-semibold uppercase tracking-wider text-foreground">
						{t(($) => $.detail.inspector_title)}
					</span>
				</div>
				<Button
					variant="ghost"
					size="icon-xs"
					className="text-muted-foreground hover:text-foreground"
					onClick={handleToggleSidebar}
					title={t(($) => $.detail.sidebar_tooltip)}
				>
					<ChevronRight className="size-4" />
				</Button>
			</div>

			{/* 2. Scrollable Body with Cards */}
			<div className="flex-1 overflow-y-auto mt-3 space-y-3.5">
				{/* Project Identity Card */}
				<div className="flex items-center gap-2.5 rounded-xl border bg-card/60 p-3 shadow-xs">
					<Popover open={iconPickerOpen} onOpenChange={setIconPickerOpen}>
						<PopoverTrigger
							render={
								<button
									type="button"
									className="text-display-sm cursor-pointer rounded-lg p-1 hover:bg-accent/60 transition-colors shrink-0"
									title={t(($) => $.detail.icon_tooltip)}
								>
									{project.icon || "📁"}
								</button>
							}
						/>
						<PopoverContent align="start" className="w-auto p-0">
							<EmojiPicker
								onSelect={(emoji) => {
									handleUpdateField({ icon: emoji });
									setIconPickerOpen(false);
								}}
							/>
						</PopoverContent>
					</Popover>
					<div className="flex-1 min-w-0">
						<TitleEditor
							key={`title-${projectId}`}
							defaultValue={project.title}
							placeholder={t(($) => $.detail.title_placeholder)}
							className="w-full text-title-sm font-semibold leading-snug tracking-tight"
							onBlur={(value) => {
								const trimmed = value.trim();
								if (trimmed && trimmed !== project.title)
									handleUpdateField({ title: trimmed });
							}}
						/>
					</div>
				</div>

				{/* Project Status & Properties Card */}
				<div className="rounded-xl border bg-card/60 p-4 space-y-3 shadow-xs">
					<div className="flex items-center justify-between">
						<button
							type="button"
							className="flex items-center gap-1.5 text-[10px] font-semibold text-muted-foreground uppercase tracking-wider hover:text-foreground transition-colors"
							onClick={() => setPropertiesOpen(!propertiesOpen)}
						>
							<span>{t(($) => $.detail.section_properties)}</span>
							<ChevronRight
								className={cn(
									"!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
									propertiesOpen && "rotate-90",
								)}
							/>
						</button>
						<span className="inline-flex items-center gap-1.5 rounded-full border bg-muted/40 px-2 py-0.5 text-[10px] font-mono font-medium text-foreground">
							<span
								className={cn(
									"size-1.5 rounded-full animate-pulse",
									statusCfg.dotColor,
								)}
							/>
							{statusLabels[project.status]}
						</span>
					</div>
					{propertiesOpen && (
						<div className="space-y-0.5 text-xs">
							<PropRow label={t(($) => $.table.status)}>
								<DropdownMenu>
									<DropdownMenuTrigger
										render={
											<button
												type="button"
												className="inline-flex items-center gap-1.5 text-caption hover:text-foreground transition-colors"
											>
												<span
													className={cn(
														"size-2 rounded-full",
														statusCfg.dotColor,
													)}
												/>
												<span>{statusLabels[project.status]}</span>
											</button>
										}
									/>
									<DropdownMenuContent align="end" className="w-44">
										{PROJECT_STATUS_ORDER.map((s) => (
											<DropdownMenuItem
												key={s}
												onClick={() =>
													handleUpdateField({ status: s as ProjectStatus })
												}
											>
												<span
													className={cn(
														"size-2 rounded-full",
														PROJECT_STATUS_CONFIG[s].dotColor,
													)}
												/>
												<span>{statusLabels[s]}</span>
												{s === project.status && (
													<Check className="ml-auto h-3.5 w-3.5" />
												)}
											</DropdownMenuItem>
										))}
									</DropdownMenuContent>
								</DropdownMenu>
							</PropRow>
							<PropRow label={t(($) => $.table.priority)}>
								<DropdownMenu>
									<DropdownMenuTrigger
										render={
											<button
												type="button"
												className="inline-flex items-center gap-1.5 text-caption hover:text-foreground transition-colors"
											>
												<PriorityIcon priority={project.priority} />
												<span>{priorityLabels[project.priority]}</span>
											</button>
										}
									/>
									<DropdownMenuContent align="end" className="w-44">
										{PROJECT_PRIORITY_ORDER.map((p) => (
											<DropdownMenuItem
												key={p}
												onClick={() =>
													handleUpdateField({ priority: p as ProjectPriority })
												}
											>
												<PriorityIcon priority={p} />
												<span>{priorityLabels[p]}</span>
												{p === project.priority && (
													<Check className="ml-auto h-3.5 w-3.5" />
												)}
											</DropdownMenuItem>
										))}
									</DropdownMenuContent>
								</DropdownMenu>
							</PropRow>
							<PropRow label={t(($) => $.table.lead)}>
								<Popover
									open={leadOpen}
									onOpenChange={(v) => {
										setLeadOpen(v);
										if (!v) setLeadFilter("");
									}}
								>
									<PopoverTrigger
										render={
											<button
												type="button"
												className="inline-flex items-center gap-1.5 text-caption hover:text-foreground transition-colors"
											>
												{project.lead_type && project.lead_id ? (
													<>
														<ActorAvatar
															actorType={project.lead_type}
															actorId={project.lead_id}
															size="sm"
															enableHoverCard
															showStatusDot
														/>
														<span className="cursor-pointer truncate max-w-[120px]">
															{getActorName(project.lead_type, project.lead_id)}
														</span>
													</>
												) : (
													<span className="text-muted-foreground">
														{t(($) => $.lead.no_lead)}
													</span>
												)}
											</button>
										}
									/>
									<PopoverContent align="end" className="w-52 p-0">
										<div className="px-2 py-1.5 border-b">
											<input
												type="text"
												value={leadFilter}
												onChange={(e) => setLeadFilter(e.target.value)}
												placeholder={t(($) => $.lead.assign_placeholder)}
												className="w-full bg-transparent text-body placeholder:text-muted-foreground outline-none"
											/>
										</div>
										<div className="p-1 max-h-60 overflow-y-auto">
											<button
												type="button"
												onClick={() => {
													handleUpdateField({ lead_type: null, lead_id: null });
													setLeadOpen(false);
												}}
												className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-body hover:bg-accent transition-colors"
											>
												<UserMinus className="h-3.5 w-3.5 text-muted-foreground" />
												<span className="text-muted-foreground">
													{t(($) => $.lead.no_lead)}
												</span>
											</button>
											{filteredMembers.length > 0 && (
												<>
													<div className="px-2 pt-2 pb-1 text-caption font-medium text-muted-foreground uppercase tracking-wider">
														{t(($) => $.lead.members_group)}
													</div>
													{filteredMembers.map((m) => (
														<button
															type="button"
															key={m.user_id}
															onClick={() => {
																handleUpdateField({
																	lead_type: "member",
																	lead_id: m.user_id,
																});
																setLeadOpen(false);
															}}
															className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-body hover:bg-accent transition-colors"
														>
															<ActorAvatar
																actorType="member"
																actorId={m.user_id}
																size="sm"
															/>
															<span>{m.name}</span>
														</button>
													))}
												</>
											)}
											{filteredAgents.length > 0 && (
												<>
													<div className="px-2 pt-2 pb-1 text-caption font-medium text-muted-foreground uppercase tracking-wider">
														{t(($) => $.lead.agents_group)}
													</div>
													{filteredAgents.map((a) => (
														<button
															type="button"
															key={a.id}
															onClick={() => {
																handleUpdateField({
																	lead_type: "agent",
																	lead_id: a.id,
																});
																setLeadOpen(false);
															}}
															className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-body hover:bg-accent transition-colors"
														>
															<ActorAvatar
																actorType="agent"
																actorId={a.id}
																size="sm"
																showStatusDot
															/>
															<span>{a.name}</span>
														</button>
													))}
												</>
											)}
											{filteredMembers.length === 0 &&
												filteredAgents.length === 0 &&
												leadFilter && (
													<div className="px-2 py-3 text-center text-body text-muted-foreground">
														{t(($) => $.lead.no_results)}
													</div>
												)}
										</div>
									</PopoverContent>
								</Popover>
							</PropRow>
							<PropRow label={t(($) => $.detail.prop_start_date)}>
								<ProjectStartDatePicker
									startDate={project.start_date}
									onUpdate={handleUpdateField}
								/>
							</PropRow>
							<PropRow label={t(($) => $.detail.prop_due_date)}>
								<ProjectDueDatePicker
									dueDate={project.due_date}
									onUpdate={handleUpdateField}
								/>
							</PropRow>
							{autonomousSnapshot?.enabled && (
								<PropRow label={t(($) => $.detail.tab_autonomous)}>
									<div className="flex items-center gap-1.5 font-medium text-emerald-500">
										<span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
										<span>
											{autonomousSnapshot.control.paused
												? t(($) => $.detail.execution_paused)
												: t(($) => $.detail.execution_autonomous)}
										</span>
									</div>
								</PropRow>
							)}
						</div>
					)}
				</div>

				{/* Sprint / Project Progress Card */}
				<div className="rounded-xl border bg-card/60 p-4 space-y-3 shadow-xs">
					<div className="flex items-center justify-between">
						<button
							type="button"
							className="flex items-center gap-1.5 text-[10px] font-semibold text-muted-foreground uppercase tracking-wider hover:text-foreground transition-colors"
							onClick={() => setProgressOpen(!progressOpen)}
						>
							<span>{t(($) => $.detail.section_progress)}</span>
							<ChevronRight
								className={cn(
									"!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
									progressOpen && "rotate-90",
								)}
							/>
						</button>
						{issueMetrics.totalCount > 0 && (
							<span className="font-mono text-xs font-semibold text-primary">
								{issueMetrics.completedCount}/{issueMetrics.totalCount} (
								{Math.round(
									(issueMetrics.completedCount / issueMetrics.totalCount) * 100,
								)}
								%)
							</span>
						)}
					</div>
					{progressOpen && (
						<div className="space-y-2.5">
							{issueMetrics.totalCount > 0 ? (
								<div className="w-full h-2 rounded-full bg-muted overflow-hidden">
									<div
										className="h-full rounded-full bg-primary transition-all duration-300"
										style={{
											width: `${Math.round((issueMetrics.completedCount / issueMetrics.totalCount) * 100)}%`,
										}}
									/>
								</div>
							) : (
								<div className="text-[11px] text-muted-foreground">
									{t(($) => $.detail.no_issues_yet)}
								</div>
							)}
						</div>
					)}
				</div>

				{/* Project Description Card */}
				<div className="rounded-xl border bg-card/60 p-4 space-y-2.5 shadow-xs">
					<button
						type="button"
						className="flex items-center gap-1.5 text-[10px] font-semibold text-muted-foreground uppercase tracking-wider hover:text-foreground transition-colors"
						onClick={() => setDescriptionOpen(!descriptionOpen)}
					>
						<span>{t(($) => $.detail.section_description)}</span>
						<ChevronRight
							className={cn(
								"!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform",
								descriptionOpen && "rotate-90",
							)}
						/>
					</button>
					{descriptionOpen && (
						<div className="space-y-1.5">
							<ContentEditor
								ref={descEditorRef}
								key={projectId}
								value={project.description || ""}
								placeholder={t(($) => $.detail.description_placeholder)}
								onUpdate={(md) =>
									handleUpdateField({ description: md || null })
								}
								debounceMs={1500}
							/>
							<p className="px-1 text-[11px] text-muted-foreground">
								{t(($) => $.detail.description_hint)}
							</p>
						</div>
					)}
				</div>

				{/* Project Root Directory Card */}
				<div className="rounded-xl border bg-card/60 p-3.5 space-y-2 shadow-xs">
					<div className="flex items-center justify-between text-[10px] font-semibold text-muted-foreground uppercase tracking-wider">
						<span>{t(($) => $.detail.prop_directory)}</span>
						{localProjectDirectory && (
							<button
								type="button"
								onClick={() => void handleOpenProjectDirectory()}
								className="text-primary hover:underline cursor-pointer lowercase first-letter:uppercase text-[11px]"
							>
								{t(($) => $.resources.local_open_tooltip)}
							</button>
						)}
					</div>
					{localProjectDirectory ? (
						<div className="flex items-center justify-between p-2 rounded-lg bg-muted/40 border border-border/50 font-mono text-[11px] text-muted-foreground">
							<div className="flex items-center gap-1.5 truncate">
								<Terminal className="size-3.5 shrink-0 text-primary" />
								<span className="truncate">{localProjectDirectory.path}</span>
							</div>
							<button
								type="button"
								onClick={() => {
									void copyText(localProjectDirectory.path).then((ok) => {
										if (ok) toast.success(t(($) => $.detail.toast_link_copied));
									});
								}}
								className="text-muted-foreground hover:text-foreground ml-1 p-1 hover:bg-accent rounded transition-colors shrink-0"
								title={t(($) => $.detail.copy_path_tooltip)}
							>
								<Copy className="size-3.5" />
							</button>
						</div>
					) : (
						<div className="px-1 text-[11px] text-muted-foreground">
							{t(($) => $.detail.project_folder_pending)}
						</div>
					)}
				</div>

				{/* Attached Squad Card */}
				<div className="rounded-xl border bg-card/60 p-4 space-y-3 shadow-xs">
					<div className="flex items-center justify-between">
						<span className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider">
							{t(($) => $.detail.section_squad)}
						</span>
						<span className="text-[10px] font-mono text-muted-foreground">
							{autonomousSnapshot?.team?.members?.length
								? `${autonomousSnapshot.team.members.length} agents`
								: project.lead_id
									? t(($) => $.detail.status_assigned)
									: `${agents.length} agents`}
						</span>
					</div>
					<div className="space-y-1.5">
						{autonomousSnapshot?.team?.members &&
						autonomousSnapshot.team.members.length > 0 ? (
							autonomousSnapshot.team.members.map((member) => (
								<div
									key={member.agent_id}
									className="flex items-center justify-between text-xs p-2 rounded-lg bg-muted/30 border border-border/30"
								>
									<div className="flex items-center gap-2 min-w-0">
										<span className="size-5 rounded bg-primary/15 text-primary font-mono font-bold text-[10px] flex items-center justify-center shrink-0 uppercase">
											{member.role.slice(0, 2)}
										</span>
										<span className="font-medium text-foreground truncate">
											{member.agent_name}
										</span>
										<span className="text-[10px] text-muted-foreground capitalize truncate">
											({member.role})
										</span>
									</div>
									<span className="text-[10px] font-mono text-emerald-500 shrink-0">
										{t(($) => $.detail.status_active)}
									</span>
								</div>
							))
						) : (
							<>
								{project.lead_type && project.lead_id ? (
									<div className="flex items-center justify-between text-xs p-2 rounded-lg bg-muted/30 border border-border/30">
										<div className="flex items-center gap-2 min-w-0">
											<ActorAvatar
												actorType={project.lead_type}
												actorId={project.lead_id}
												size="sm"
												showStatusDot
											/>
											<div className="flex flex-col min-w-0">
												<span className="font-medium text-foreground truncate">
													{getActorName(project.lead_type, project.lead_id)}
												</span>
												<span className="text-[10px] text-muted-foreground">
													{t(($) => $.detail.status_lead)}
												</span>
											</div>
										</div>
										<span className="text-[10px] font-mono text-primary shrink-0">
											{t(($) => $.detail.status_assigned)}
										</span>
									</div>
								) : null}
								{agents.slice(0, 3).map((agent) => (
									<div
										key={agent.id}
										className="flex items-center justify-between text-xs p-2 rounded-lg bg-muted/30 border border-border/30"
									>
										<div className="flex items-center gap-2 min-w-0">
											<ActorAvatar
												actorType="agent"
												actorId={agent.id}
												size="sm"
												showStatusDot
											/>
											<span className="font-medium text-foreground truncate">
												{agent.name}
											</span>
										</div>
										<span className="text-[10px] font-mono text-muted-foreground shrink-0">
											{t(($) => $.detail.status_ready)}
										</span>
									</div>
								))}
							</>
						)}
					</div>
				</div>

				{/* Resources Card */}
				<div className="rounded-xl border bg-card/60 p-4 space-y-3 shadow-xs">
					<ProjectResourcesSection projectId={projectId} />
				</div>
			</div>
		</div>
	);

	return (
		<>
			<ResizablePanelGroup
				orientation="horizontal"
				className="flex-1 min-h-0"
				defaultLayout={defaultLayout}
				onLayoutChanged={onLayoutChanged}
			>
				<ResizablePanel id="content" minSize="50%">
					<div
						ref={rightSidebarShortcutTargetRef}
						className="flex h-full flex-col"
					>
						<BreadcrumbHeader
							segments={[
								{
									href: wsPaths.projects(),
									label: t(($) => $.detail.breadcrumb_fallback),
								},
							]}
							leaf={
								<span className="truncate font-medium text-foreground">
									{project.title}
								</span>
							}
							actions={
								<>
									<Button
										variant="ghost"
										size="icon-sm"
										className={cn(
											"text-muted-foreground",
											isPinned && "text-foreground",
										)}
										title={
											isPinned
												? t(($) => $.detail.unpin_tooltip)
												: t(($) => $.detail.pin_tooltip)
										}
										onClick={() => {
											if (isPinned) {
												deletePinMut.mutate({
													itemType: "project",
													itemId: projectId,
												});
											} else {
												createPin.mutate({
													item_type: "project",
													item_id: projectId,
												});
											}
										}}
									>
										{isPinned ? <PinOff /> : <Pin />}
									</Button>
									<DropdownMenu>
										<DropdownMenuTrigger
											render={
												<Button
													variant="ghost"
													size="icon-sm"
													className="text-muted-foreground"
												>
													<MoreHorizontal />
												</Button>
											}
										/>
										<DropdownMenuContent align="end" className="w-auto">
											<DropdownMenuItem
												onClick={() => {
													void copyText(
														router.getShareableUrl(currentPath(router)),
													).then((ok) => {
														if (ok)
															toast.success(
																t(($) => $.detail.toast_link_copied),
															);
													});
												}}
											>
												<Link2 className="h-3.5 w-3.5" />
												{t(($) => $.detail.copy_link)}
											</DropdownMenuItem>
											{isWorkspaceAdmin && (
												<>
													<DropdownMenuSeparator />
													<DropdownMenuItem
														variant="destructive"
														onClick={() => setDeleteDialogOpen(true)}
													>
														<Trash2 className="h-3.5 w-3.5" />
														{t(($) => $.detail.delete_action)}
													</DropdownMenuItem>
												</>
											)}
										</DropdownMenuContent>
									</DropdownMenu>
									<Tooltip>
										<TooltipTrigger
											render={
												<Button
													variant={sidebarOpen ? "secondary" : "ghost"}
													size="icon-sm"
													className={sidebarOpen ? "" : "text-muted-foreground"}
													onClick={handleToggleSidebar}
												>
													<PanelRight />
												</Button>
											}
										/>
										<TooltipContent side="bottom">
											{t(($) => $.detail.sidebar_tooltip)}
										</TooltipContent>
									</Tooltip>
								</>
							}
						/>

						<div className="flex items-center gap-1 border-b px-3 py-1.5 bg-muted/10">
							<Button
								type="button"
								size="sm"
								variant={contentView === "cockpit" ? "secondary" : "ghost"}
								onClick={() => handleContentViewChange("cockpit")}
								className={cn(
									"gap-1.5 text-xs font-medium transition-all",
									contentView === "cockpit" && "bg-background shadow-xs font-semibold text-foreground",
								)}
							>
								<LayoutDashboard className="size-3.5 text-primary" />
								{t(($) => $.detail.tab_cockpit)}
							</Button>
							<Button
								type="button"
								size="sm"
								variant={contentView === "issues" ? "secondary" : "ghost"}
								onClick={() => handleContentViewChange("issues")}
								className={cn(
									"gap-1.5 text-xs font-medium transition-all",
									contentView === "issues" && "bg-background shadow-xs font-semibold text-foreground",
								)}
							>
								<ListTodo className="size-3.5" />
								{t(($) => $.detail.tab_issues)}
							</Button>
							<Button
								type="button"
								size="sm"
								variant={contentView === "autonomous" ? "secondary" : "ghost"}
								onClick={() => handleContentViewChange("autonomous")}
								className={cn(
									"gap-1.5 text-xs font-medium transition-all",
									contentView === "autonomous" && "bg-background shadow-xs font-semibold text-foreground",
								)}
							>
								<Sparkles className="size-3.5 text-purple-500" />
								{t(($) => $.detail.tab_autonomous)}
							</Button>
							<Button
								type="button"
								size="sm"
								variant={contentView === "report" ? "secondary" : "ghost"}
								onClick={() => handleContentViewChange("report")}
								className={cn(
									"gap-1.5 text-xs font-medium transition-all",
									contentView === "report" && "bg-background shadow-xs font-semibold text-foreground",
								)}
							>
								<BarChart3 className="size-3.5" />
								{t(($) => $.detail.tab_reports)}
							</Button>
						</div>

						<div className="flex h-full min-h-0 flex-1 flex-col">
							{contentView === "cockpit" ? (
								<ProjectCockpitView
									project={project}
									snapshot={autonomousSnapshot}
									changeRequests={leaderChanges.data?.items}
									canControl={isWorkspaceAdmin}
									onNavigateTab={handleContentViewChange}
									onOpenLeaderChat={handleOpenLeaderChat}
									onConfirmTeam={handleConfirmTeam}
									isConfirmingTeam={confirmTeam.isPending}
									onApproveChange={(id) =>
										approveLeaderChange.mutate({
											projectId,
											changeRequestId: id,
										})
									}
									onRejectChange={(id) =>
										rejectLeaderChange.mutate({
											projectId,
											changeRequestId: id,
										})
									}
									onResolveEscalation={(id) =>
										resolveEscalation.mutate({
											projectId,
											escalationId: id,
											decision: "approved",
										})
									}
									isApproving={approveLeaderChange.isPending}
									isRejecting={rejectLeaderChange.isPending}
								/>
							) : contentView === "issues" ? (
								<IssueSurface
									scope={issueScope}
									modes={["board", "list", "table", "swimlane", "gantt"]}
								/>
							) : contentView === "autonomous" ? (
								<AutonomousControlCenter
									projectId={projectId}
									canControl={isWorkspaceAdmin}
								/>
							) : (
								<ProjectReport projectId={projectId} />
							)}
						</div>
					</div>
				</ResizablePanel>
				{!isMobile && <ResizableHandle />}
				{!isMobile && (
					<ResizablePanel
						id="sidebar"
						{...rightSidebarPanelMotionProps}
						data-right-sidebar-motion={
							desktopSidebarMotionEnabled ? "enabled" : undefined
						}
						defaultSize={desktopSidebarOpen ? 320 : 0}
						minSize={260}
						maxSize={420}
						collapsible
						groupResizeBehavior="preserve-pixel-size"
						panelRef={sidebarRef}
						onResize={handleDesktopSidebarResize}
					>
						<AnimatedRightSidebar
							open={desktopSidebarVisualOpen}
							motionEnabled={desktopSidebarMotionEnabled}
						>
							{sidebarContent}
						</AnimatedRightSidebar>
					</ResizablePanel>
				)}
				{isMobile && (
					<Sheet open={mobileSidebarOpen} onOpenChange={setMobileSidebarOpen}>
						<SheetContent
							side="right"
							showCloseButton={false}
							className="w-[320px] p-0 flex flex-col overflow-hidden"
						>
							{sidebarContent}
						</SheetContent>
					</Sheet>
				)}
			</ResizablePanelGroup>

			{/* Delete confirmation */}
			{isWorkspaceAdmin && (
				<AlertDialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
					<AlertDialogContent>
						<AlertDialogHeader>
							<AlertDialogTitle>
								{t(($) => $.delete_dialog.title)}
							</AlertDialogTitle>
							<AlertDialogDescription>
								{t(($) => $.delete_dialog.description)}
							</AlertDialogDescription>
						</AlertDialogHeader>
						<AlertDialogFooter>
							<AlertDialogCancel>
								{t(($) => $.delete_dialog.cancel)}
							</AlertDialogCancel>
							<AlertDialogAction
								onClick={handleDelete}
								className="bg-destructive text-white hover:bg-destructive/90"
							>
								{t(($) => $.delete_dialog.confirm)}
							</AlertDialogAction>
						</AlertDialogFooter>
					</AlertDialogContent>
				</AlertDialog>
			)}
		</>
	);
}
