import { useState, useEffect, useMemo } from "react";
import {
  Activity,
  Check,
  CheckCircle2,
  Play,
  ShieldAlert,
  Sparkles,
  X,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { useWorkspacePaths } from "@multica/core/paths";
import { useT, useLocale } from "../../i18n";
import { AppLink } from "../../navigation";
import type {
  VirtualOfficeSceneData,
  OfficeAgentDesk,
  OfficeZoneKey,
  OfficeReplayEvent,
  OfficeBlocker,
  OfficeActivityKind,
} from "../types";
import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
} from "@multica/core/types";
import type { TFunction } from "i18next";

interface OfficeInspectorDrawerProps {
  scene: VirtualOfficeSceneData;
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[] | null;
  selectedDesk?: OfficeAgentDesk | null;
  selectedStationKey?: OfficeZoneKey | null;
  activeReplay?: OfficeReplayEvent | null;
  canControl?: boolean;
  isOpen: boolean;
  onClose: () => void;
  onReplayEvent?: (event: OfficeReplayEvent) => void;
  onApprovePlan?: () => void;
  onApproveChange?: (changeRequestId: string) => void;
  onRejectChange?: (changeRequestId: string) => void;
  onResolveEscalation?: (escalationId: string) => void;
  onBlockerAction?: (blocker: OfficeBlocker) => void;
  className?: string;
}

export function OfficeInspectorDrawer({
  scene,
  snapshot,
  changeRequests = [],
  selectedDesk,
  selectedStationKey,
  activeReplay,
  canControl,
  isOpen,
  onClose,
  onReplayEvent,
  onApprovePlan,
  onApproveChange,
  onRejectChange,
  onResolveEscalation,
  onBlockerAction,
  className,
}: OfficeInspectorDrawerProps) {
  const { t } = useT("projects");
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const timeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }),
    [locale],
  );
  const [activeTab, setActiveTab] = useState<"feed" | "inspector">(
    selectedDesk || selectedStationKey === "approval_lounge" ? "inspector" : "feed",
  );

  // If a desk or approval station was selected, focus on inspector tab
  useEffect(() => {
    if (selectedDesk || selectedStationKey === "approval_lounge") {
      setActiveTab("inspector");
    }
  }, [selectedDesk, selectedStationKey]);

  if (!isOpen) return null;

  const pendingChanges = (changeRequests ?? []).filter((cr) => cr.state === "pending");
  const pendingEscalations = (snapshot?.escalations ?? []).filter((e) => e.status === "pending");
  const planAwaiting = snapshot?.plan?.status === "awaiting_approval";

  return (
    <aside
      aria-label={t(($) => $.office.inspector.title)}
      className={cn(
        "absolute inset-0 z-30 flex min-w-0 shrink-0 flex-col border-border/80 bg-background/95 shadow-xl backdrop-blur-md transition-all duration-200 motion-reduce:transition-none md:relative md:inset-auto md:z-auto md:w-84 md:border-l",
        className,
      )}
    >
      {/* Drawer Header with Dual Tabs & Close */}
      <div className="flex items-center justify-between border-b px-3 py-2.5">
        <div className="flex items-center gap-1 rounded-md bg-muted/60 p-0.5 text-caption">
          <button
            type="button"
            aria-pressed={activeTab === "feed"}
            aria-label={t(($) => $.office.spectator_feed)}
            onClick={() => setActiveTab("feed")}
            className={cn(
              "flex items-center gap-1.5 rounded px-2.5 py-1 text-caption font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none",
              activeTab === "feed"
                ? "bg-background text-foreground shadow-xs font-semibold"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Activity className="size-3.5 text-info" />
            <span>{t(($) => $.office.spectator_feed)}</span>
          </button>

          <button
            type="button"
            aria-pressed={activeTab === "inspector"}
            aria-label={t(($) => $.office.inspector.title)}
            onClick={() => setActiveTab("inspector")}
            className={cn(
              "flex items-center gap-1.5 rounded px-2.5 py-1 text-caption font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none",
              activeTab === "inspector"
                ? "bg-background text-foreground shadow-xs font-semibold"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Sparkles className="size-3.5 text-warning" />
            <span>{t(($) => $.office.inspector.title)}</span>
            {(scene.pendingApprovalsCount > 0 || Boolean(selectedDesk)) && (
              <span className="size-1.5 rounded-full bg-warning" />
            )}
          </button>
        </div>

        <Button
          type="button"
          size="icon-xs"
          variant="ghost"
          onClick={onClose}
          aria-label={t(($) => $.office.close_inspector)}
          className="text-muted-foreground hover:text-foreground"
        >
          <X className="size-4" />
        </Button>
      </div>

      {/* Tab 1: Spectator Activity Feed */}
      {activeTab === "feed" && (
        <div className="flex-1 overflow-y-auto p-3">
          <div className="mb-2 flex items-center justify-between text-caption text-muted-foreground">
            <span className="font-medium">{t(($) => $.office.inspector.live_activity_stream)}</span>
            <span className="text-micro">
              {t(($) => $.office.inspector.events_count, { count: scene.recentEvents.length })}
            </span>
          </div>

          {scene.recentEvents.length === 0 ? (
            <div className="flex flex-col items-center justify-center p-8 text-center text-caption text-muted-foreground">
              <Activity className="mb-2 size-6 stroke-1 text-faint-foreground" />
              <p>{t(($) => $.office.empty_activity)}</p>
            </div>
          ) : (
            <div className="space-y-2">
              {scene.recentEvents.map((evt) => {
                const isCurrentReplay = activeReplay?.id === evt.id;

                return (
                  <div
                    key={evt.id}
                    className={cn(
                      "group flex min-w-0 flex-col gap-1.5 rounded-lg border p-2.5 text-caption transition-colors",
                      isCurrentReplay
                        ? "border-warning/60 bg-warning/5 ring-1 ring-warning/30"
                        : "border-border/60 bg-card/60 hover:border-border hover:bg-card",
                    )}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-1.5 font-medium leading-tight text-foreground">
                        <span className="size-1.5 shrink-0 rounded-full bg-info" />
                        <span className="line-clamp-2">{getActivityLabel(evt.type, t)}</span>
                      </div>
                      <span className="shrink-0 font-mono text-micro text-muted-foreground">
                        {formatTimestamp(evt.timestamp, timeFormatter)}
                      </span>
                    </div>

                    {evt.fromStation && evt.toStation && (
                      <div className="mt-1 flex items-center justify-between gap-2 border-t border-border/40 pt-1.5 text-micro">
                        <div className="flex min-w-0 items-center gap-1 text-muted-foreground">
                          <span className="truncate font-medium">{t(($) => $.office.zones[evt.fromStation!])}</span>
                          <span aria-hidden="true">→</span>
                          <span className="truncate font-medium">{t(($) => $.office.zones[evt.toStation!])}</span>
                        </div>
                        {onReplayEvent && (
                          <Button
                            type="button"
                            size="xs"
                            variant="ghost"
                            onClick={() => onReplayEvent(evt)}
                            className="h-7 shrink-0 gap-1 px-1.5 text-caption text-warning hover:bg-warning/10 hover:text-warning"
                          >
                            <Play className="size-3 fill-warning" />
                            <span>{t(($) => $.office.replay_on_map)}</span>
                          </Button>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}

      {/* Tab 2: Selected Item & Quick Approvals */}
      {activeTab === "inspector" && (
        <div className="min-w-0 flex-1 space-y-4 overflow-y-auto p-3">
          {/* Section A: Selected Agent Details */}
          {selectedDesk && (
              <div className="space-y-3 rounded-lg border border-border bg-card p-3 shadow-xs">
              <div className="flex items-center gap-2.5">
                <ActorAvatar
                  name={selectedDesk.agentName}
                  initials={selectedDesk.agentName.slice(0, 2).toUpperCase()}
                  avatarUrl={resolvePublicFileUrl(selectedDesk.avatarUrl)}
                  size="md"
                  className="size-8"
                />
                <div className="min-w-0 flex-1">
                  <div className="font-semibold text-body leading-tight text-foreground">
                    {selectedDesk.agentName}
                  </div>
                  <div className="text-caption leading-tight text-muted-foreground">
                    {selectedDesk.role}
                  </div>
                </div>
                <Badge variant="outline" className="text-micro">
                  {getDeskStatusLabel(selectedDesk.status, t)}
                </Badge>
              </div>

              {selectedDesk.currentTaskTitle && (
                <div className="space-y-1 rounded-md bg-muted/50 p-2 text-caption">
                  <div className="text-micro font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.office.active_task)}
                  </div>
                  <p className="text-foreground font-medium leading-relaxed">
                    {selectedDesk.activeTask?.title || selectedDesk.currentTaskTitle}
                  </p>
                </div>
              )}

              {/* Live Tool Invocation Stream */}
              {selectedDesk.activeTool && (
                <div className="space-y-1 rounded-md border border-info/30 bg-muted p-2 font-mono text-caption">
                  <div className="flex items-center gap-1.5 text-micro font-semibold text-info">
                    <span className="size-2 rounded-full bg-info motion-safe:animate-pulse motion-reduce:animate-none" />
                    <span>{t(($) => $.office.active_tool_call)}</span>
                  </div>
                  <div className="flex min-w-0 items-center gap-1 text-micro text-foreground">
                    <span className="shrink-0 font-bold text-info">{selectedDesk.activeTool.name}:</span>
                    <span className="truncate text-muted-foreground">{selectedDesk.activeTool.target}</span>
                  </div>
                </div>
              )}

              {/* Scoped Allowed Skills */}
              {(selectedDesk.allowedSkills?.length ?? 0) > 0 && (
                <div className="space-y-1.5">
                  <div className="text-micro font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.office.allowed_skills_count, { count: selectedDesk.allowedSkills?.length ?? 0 })}
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {selectedDesk.allowedSkills?.map((skill) => (
                      <Badge
                        key={skill}
                        variant="secondary"
                        className="border-warning/20 bg-warning/10 font-mono text-micro text-warning"
                      >
                        {skill}
                      </Badge>
                    ))}
                  </div>
                </div>
              )}

              {/* Queued Tasks List in Drawer */}
              {(selectedDesk.queuedTasks?.length ?? 0) > 0 && (
                <div className="space-y-1.5 rounded-md border border-border/50 bg-muted/30 p-2 text-caption">
                  <div className="flex items-center justify-between text-micro font-semibold uppercase tracking-wide text-muted-foreground">
                    <span>{t(($) => $.office.queued_header)}</span>
                    <span className="font-mono text-warning">
                      {selectedDesk.queuedTasks.length}
                    </span>
                  </div>
                  <ul className="space-y-1">
                    {selectedDesk.queuedTasks.map((task, idx) => (
                      <li
                        key={task.id}
                        className="flex items-start gap-1.5 text-micro text-muted-foreground"
                      >
                        <span className="shrink-0 font-mono font-medium text-warning">
                          {idx + 1}.
                        </span>
                        <span className="line-clamp-2 text-foreground">{task.title}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}

            </div>
          )}

          {scene.blockers.length > 0 && (
            <section aria-labelledby="office-blockers-heading" className="space-y-2">
              <div className="flex items-center justify-between">
                <h2 id="office-blockers-heading" className="text-caption font-semibold text-foreground">
                  {t(($) => $.office.blockers.title)}
                </h2>
                <Badge variant="outline" className="text-micro">{scene.blockers.length}</Badge>
              </div>
              <div className="space-y-2">
                {scene.blockers.map((blocker) => {
                  const reason = t(($) => $.office.blockers.reasons[blocker.kind], {
                    dependencyCount: blocker.params?.dependencyCount ?? 1,
                  });
                  const actionLabel = (() => {
                    switch (blocker.action.kind) {
                      case "open_issue": return t(($) => $.office.blockers.actions.open_issue);
                      case "retry_run":
                      case "retry_action": return t(($) => $.office.blockers.actions.retry);
                      case "resolve_escalation": return t(($) => $.office.blockers.actions.resolve_escalation);
                      case "approve_plan": return t(($) => $.office.blockers.actions.approve_plan);
                      case "none": return null;
                    }
                  })();
                  const severityLabel = t(($) => $.office.blockers.severity[blocker.severity]);

                  return (
                    <article
                      key={blocker.id}
                      className={cn(
                        "space-y-2 rounded-lg border p-3 text-caption",
                        blocker.severity === "error" && "border-destructive/30 bg-destructive/5",
                        blocker.severity === "warning" && "border-warning/30 bg-warning/5",
                        blocker.severity === "waiting" && "border-info/30 bg-info/5",
                      )}
                    >
                      <div className="flex items-start justify-between gap-2">
                        <p className="min-w-0 font-medium text-foreground">{reason}</p>
                        <Badge variant="outline" className="shrink-0 text-micro">
                          {severityLabel}
                        </Badge>
                      </div>
                      {actionLabel && blocker.action.kind === "open_issue" && (
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          render={<AppLink href={paths.issueDetail(blocker.action.targetId)} />}
                          nativeButton={false}
                          className="w-full gap-1.5 text-caption"
                        >
                          <ShieldAlert className="size-3.5" aria-hidden="true" />
                          <span>{actionLabel}</span>
                        </Button>
                      )}
                      {canControl && onBlockerAction && actionLabel && blocker.action.kind !== "open_issue" && (
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          onClick={() => onBlockerAction(blocker)}
                          className="w-full gap-1.5 text-caption"
                        >
                          <ShieldAlert className="size-3.5" aria-hidden="true" />
                          <span>{actionLabel}</span>
                        </Button>
                      )}
                    </article>
                  );
                })}
              </div>
            </section>
          )}

          {/* Section B: Pending Approvals */}
          <div className="space-y-2">
            <div className="flex items-center justify-between text-caption font-semibold text-foreground">
              <div className="flex items-center gap-1.5">
                <ShieldAlert className="size-3.5 text-warning" />
                <span>{t(($) => $.office.zones.approval_lounge)}</span>
              </div>
              <Badge variant="secondary" className="text-micro">
                {t(($) => $.office.inspector.pending_count, { count: scene.pendingApprovalsCount })}
              </Badge>
            </div>

            {/* Plan Awaiting Approval */}
            {planAwaiting && (
              <div className="space-y-2.5 rounded-lg border border-warning/30 bg-warning/5 p-3 text-caption">
                <div className="flex items-center justify-between">
                  <Badge className="h-5 bg-warning px-1.5 text-micro text-foreground">
                    {t(($) => $.office.inspector.plan_revision, { revision: snapshot?.plan?.revision ?? 1 })}
                  </Badge>
                  <span className="font-medium text-micro text-warning">
                    {t(($) => $.office.inspector.awaiting_approval)}
                  </span>
                </div>
                {snapshot?.plan?.goal && <p className="font-medium text-foreground">{snapshot.plan.goal}</p>}
                {canControl && onApprovePlan && (
                  <Button
                    type="button"
                    size="sm"
                    onClick={onApprovePlan}
                    className="w-full gap-1.5 text-caption font-semibold"
                  >
                    <Check className="size-3.5" />
                    <span>{t(($) => $.office.inspector.approve_plan)}</span>
                  </Button>
                )}
              </div>
            )}

            {/* Pending Leader Change Requests */}
            {pendingChanges.map((cr) => (
              <div
                key={cr.id}
                className="space-y-2 rounded-lg border border-border bg-card p-3 text-caption"
              >
                <div className="flex items-center justify-between">
                  <span className="font-semibold text-foreground">{t(($) => $.office.inspector.change_request)}</span>
                  <span className="text-micro text-muted-foreground">{t(($) => $.office.inspector.pending)}</span>
                </div>
                <p className="text-muted-foreground text-micro leading-relaxed">
                  {cr.request_text}
                </p>
                {canControl && (
                  <div className="flex items-center gap-2 pt-1">
                    {onApproveChange && (
                      <Button
                        type="button"
                        size="xs"
                        onClick={() => onApproveChange(cr.id)}
                        className="flex-1 gap-1 text-caption"
                      >
                        <Check className="size-3" />
                        <span>{t(($) => $.office.quick_approve)}</span>
                      </Button>
                    )}
                    {onRejectChange && (
                      <Button
                        type="button"
                        size="xs"
                        variant="ghost"
                        onClick={() => onRejectChange(cr.id)}
                        className="text-caption text-destructive hover:bg-destructive/10"
                      >
                        <span>{t(($) => $.office.quick_reject)}</span>
                      </Button>
                    )}
                  </div>
                )}
              </div>
            ))}

            {/* Pending Escalations */}
            {pendingEscalations.map((esc) => (
              <div
                key={esc.id}
                className="space-y-2 rounded-lg border border-warning/30 bg-warning/5 p-3 text-caption"
              >
                <div className="flex items-center justify-between gap-2 font-semibold text-warning">
                  <span>{t(($) => $.office.inspector.escalation_pending)}</span>
                  <Badge variant="outline" className="shrink-0 text-micro">
                    {t(($) => $.office.blockers.severity.warning)}
                  </Badge>
                </div>
                <p className="text-foreground text-micro">{esc.summary}</p>
                {canControl && onResolveEscalation && (
                  <Button
                    type="button"
                    size="xs"
                    onClick={() => onResolveEscalation(esc.id)}
                    className="w-full gap-1 text-caption"
                  >
                    <Check className="size-3" />
                    <span>{t(($) => $.office.inspector.resolve_escalation)}</span>
                  </Button>
                )}
              </div>
            ))}

            {scene.pendingApprovalsCount === 0 && scene.blockers.length === 0 && !selectedDesk && (
              <div className="space-y-1 rounded-lg border border-dashed p-6 text-center text-caption text-muted-foreground">
                <CheckCircle2 className="mx-auto size-5 text-success" />
                <p className="font-medium text-foreground">{t(($) => $.office.inspector.all_clear)}</p>
                <p className="text-micro">{t(($) => $.office.inspector.no_pending)}</p>
              </div>
            )}
          </div>
        </div>
      )}
    </aside>
  );
}

function getActivityLabel(type: OfficeActivityKind, t: TFunction<"projects">) {
  switch (type) {
    case "task.dispatched": return t(($) => $.office.activities.task_dispatched);
    case "task.completed": return t(($) => $.office.activities.task_completed);
    case "task.failed": return t(($) => $.office.activities.task_failed);
    case "workflow.review_requested": return t(($) => $.office.activities.review_requested);
    case "workflow.reviewed": return t(($) => $.office.activities.reviewed);
    case "plan.approved": return t(($) => $.office.activities.plan_approved);
    case "escalation.created": return t(($) => $.office.activities.escalation_created);
  }
}

function getDeskStatusLabel(status: OfficeAgentDesk["status"], t: TFunction<"projects">) {
  switch (status) {
    case "idle": return t(($) => $.office.desk_status.standby);
    case "running": return t(($) => $.office.desk_status.coding);
    case "debugging": return t(($) => $.office.desk_status.debugging);
    case "verifying": return t(($) => $.office.desk_status.testing);
    case "blocked": return t(($) => $.office.desk_status.blocked);
    case "failed": return t(($) => $.office.desk_status.failed);
    case "approved": return t(($) => $.office.desk_status.approved);
  }
}

function formatTimestamp(timestamp: string, formatter: Intl.DateTimeFormat) {
  const date = new Date(timestamp);
  return Number.isNaN(date.valueOf()) ? "" : formatter.format(date);
}
