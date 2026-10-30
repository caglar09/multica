import { useState, useEffect } from "react";
import {
  Activity,
  Check,
  CheckCircle2,
  MessageSquare,
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
import { useT } from "../../i18n";
import type {
  VirtualOfficeSceneData,
  OfficeAgentDesk,
  OfficeZoneKey,
  OfficeReplayEvent,
} from "../types";
import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
} from "@multica/core/types";

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
  onOpenLeaderChat?: () => void;
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
  onOpenLeaderChat,
  className,
}: OfficeInspectorDrawerProps) {
  const { t } = useT("projects");
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
      className={cn(
        "flex w-84 shrink-0 flex-col border-l border-border/80 bg-background/95 shadow-xl backdrop-blur-md transition-all duration-200 select-none",
        className,
      )}
    >
      {/* Drawer Header with Dual Tabs & Close */}
      <div className="flex items-center justify-between border-b px-3 py-2.5">
        <div className="flex items-center gap-1 rounded-md bg-muted/60 p-0.5 text-xs">
          <button
            type="button"
            onClick={() => setActiveTab("feed")}
            className={cn(
              "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
              activeTab === "feed"
                ? "bg-background text-foreground shadow-xs font-semibold"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Activity className="size-3.5 text-cyan-500" />
            <span>{t(($) => $.office.spectator_feed)}</span>
          </button>

          <button
            type="button"
            onClick={() => setActiveTab("inspector")}
            className={cn(
              "flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium transition-colors",
              activeTab === "inspector"
                ? "bg-background text-foreground shadow-xs font-semibold"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Sparkles className="size-3.5 text-amber-500" />
            <span>{t(($) => $.office.inspector.title)}</span>
            {(scene.pendingApprovalsCount > 0 || Boolean(selectedDesk)) && (
              <span className="size-1.5 rounded-full bg-amber-500" />
            )}
          </button>
        </div>

        <Button
          type="button"
          size="icon-xs"
          variant="ghost"
          onClick={onClose}
          className="text-muted-foreground hover:text-foreground"
        >
          <X className="size-4" />
        </Button>
      </div>

      {/* Tab 1: Spectator Activity Feed */}
      {activeTab === "feed" && (
        <div className="flex-1 overflow-y-auto p-3">
          <div className="mb-2 flex items-center justify-between text-xs text-muted-foreground">
            <span className="font-medium">{t(($) => $.office.inspector.live_activity_stream)}</span>
            <span className="text-[10px]">
              {t(($) => $.office.inspector.events_count, { count: scene.recentEvents.length })}
            </span>
          </div>

          {scene.recentEvents.length === 0 ? (
            <div className="flex flex-col items-center justify-center p-8 text-center text-xs text-muted-foreground">
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
                      "group flex flex-col gap-1.5 rounded-lg border p-2.5 text-xs transition-all",
                      isCurrentReplay
                        ? "border-amber-500/60 bg-amber-500/5 ring-1 ring-amber-500/30"
                        : "border-border/60 bg-card/60 hover:border-border hover:bg-card",
                    )}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-1.5 font-medium leading-tight text-foreground">
                        <span className="size-1.5 shrink-0 rounded-full bg-cyan-400" />
                        <span className="line-clamp-2">{evt.title}</span>
                      </div>
                      <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                        {evt.timestamp ? new Date(evt.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : ""}
                      </span>
                    </div>

                    {evt.payloadSummary && (
                      <p className="line-clamp-2 text-[11px] text-muted-foreground">
                        {evt.payloadSummary}
                      </p>
                    )}

                    <div className="mt-1 flex items-center justify-between border-t border-border/40 pt-1.5 text-[10.5px]">
                      <div className="flex items-center gap-1 text-muted-foreground">
                        <span className="font-medium capitalize">{evt.fromStation.replace("_", " ")}</span>
                        <span>→</span>
                        <span className="font-medium capitalize">{evt.toStation.replace("_", " ")}</span>
                      </div>

                      {onReplayEvent && (
                        <Button
                          type="button"
                          size="xs"
                          variant="ghost"
                          onClick={() => onReplayEvent(evt)}
                          className="h-5 gap-1 px-1.5 text-[10.5px] text-amber-500 hover:bg-amber-500/10 hover:text-amber-400"
                        >
                          <Play className="size-2.5 fill-amber-500" />
                          <span>{t(($) => $.office.replay_on_map)}</span>
                        </Button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}

      {/* Tab 2: Selected Item & Quick Approvals */}
      {activeTab === "inspector" && (
        <div className="flex-1 overflow-y-auto p-3 space-y-4">
          {/* Section A: Selected Agent Details */}
          {selectedDesk && (
            <div className="rounded-lg border bg-card p-3 shadow-xs space-y-3">
              <div className="flex items-center gap-2.5">
                <ActorAvatar
                  name={selectedDesk.agentName}
                  initials={selectedDesk.agentName.slice(0, 2).toUpperCase()}
                  avatarUrl={resolvePublicFileUrl(selectedDesk.avatarUrl)}
                  size="md"
                  className="size-8"
                />
                <div className="min-w-0 flex-1">
                  <div className="font-semibold text-sm leading-tight text-foreground">
                    {selectedDesk.agentName}
                  </div>
                  <div className="text-xs text-muted-foreground leading-tight">
                    {selectedDesk.role}
                  </div>
                </div>
                <Badge variant="outline" className="text-[10px] capitalize">
                  {selectedDesk.status}
                </Badge>
              </div>

              {selectedDesk.currentTaskTitle && (
                <div className="rounded-md bg-muted/50 p-2 text-xs space-y-1">
                  <div className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wide">
                    {t(($) => $.office.active_task)}
                  </div>
                  <p className="text-foreground font-medium leading-relaxed">
                    {selectedDesk.activeTask?.title || selectedDesk.currentTaskTitle}
                  </p>
                </div>
              )}

              {/* Queued Tasks List in Drawer */}
              {(selectedDesk.queuedTasks?.length ?? 0) > 0 && (
                <div className="rounded-md bg-muted/30 p-2 text-xs space-y-1.5 border border-border/50">
                  <div className="flex items-center justify-between text-[10px] font-semibold text-muted-foreground uppercase tracking-wide">
                    <span>{t(($) => $.office.queued_header)}</span>
                    <span className="text-amber-500 font-mono">
                      {selectedDesk.queuedTasks.length}
                    </span>
                  </div>
                  <ul className="space-y-1">
                    {selectedDesk.queuedTasks.map((task, idx) => (
                      <li
                        key={task.id}
                        className="flex items-start gap-1.5 text-[11px] text-muted-foreground"
                      >
                        <span className="font-mono text-amber-500 font-medium shrink-0">
                          {idx + 1}.
                        </span>
                        <span className="line-clamp-2 text-foreground/80">{task.title}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}

              {onOpenLeaderChat && (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={onOpenLeaderChat}
                  className="w-full gap-1.5 text-xs"
                >
                  <MessageSquare className="size-3.5" />
                  <span>{t(($) => $.office.inspector.open_leader_chat)}</span>
                </Button>
              )}
            </div>
          )}

          {/* Section B: Pending Approvals (Human-in-the-Loop Gate) */}
          <div className="space-y-2">
            <div className="flex items-center justify-between text-xs font-semibold text-foreground">
              <div className="flex items-center gap-1.5">
                <ShieldAlert className="size-3.5 text-amber-500" />
                <span>{t(($) => $.office.zones.approval_lounge)}</span>
              </div>
              <Badge variant="secondary" className="text-[10px]">
                {t(($) => $.office.inspector.pending_count, { count: scene.pendingApprovalsCount })}
              </Badge>
            </div>

            {/* Plan Awaiting Approval */}
            {planAwaiting && (
              <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs space-y-2.5">
                <div className="flex items-center justify-between">
                  <Badge className="bg-amber-500 text-white text-[10px] h-4 px-1.5">
                    {t(($) => $.office.inspector.plan_revision, { revision: snapshot?.plan?.revision ?? 1 })}
                  </Badge>
                  <span className="text-[10px] text-amber-600 dark:text-amber-400 font-medium">
                    {t(($) => $.office.inspector.awaiting_approval)}
                  </span>
                </div>
                <p className="font-medium text-foreground">
                  {snapshot?.plan?.goal || "Autonomous Project Roadmap Plan"}
                </p>
                {canControl && onApprovePlan && (
                  <Button
                    type="button"
                    size="sm"
                    onClick={onApprovePlan}
                    className="w-full gap-1.5 bg-amber-500 text-xs font-semibold text-white hover:bg-amber-600"
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
                className="rounded-lg border border-border bg-card p-3 text-xs space-y-2"
              >
                <div className="flex items-center justify-between">
                  <span className="font-semibold text-foreground">{t(($) => $.office.inspector.change_request)}</span>
                  <span className="text-[10px] text-muted-foreground">{t(($) => $.office.inspector.pending)}</span>
                </div>
                <p className="text-muted-foreground text-[11.5px] leading-relaxed">
                  {cr.request_text}
                </p>
                {canControl && (
                  <div className="flex items-center gap-2 pt-1">
                    {onApproveChange && (
                      <Button
                        type="button"
                        size="xs"
                        onClick={() => onApproveChange(cr.id)}
                        className="flex-1 gap-1 text-xs"
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
                        className="text-xs text-destructive hover:bg-destructive/10"
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
                className="rounded-lg border border-rose-500/30 bg-rose-500/5 p-3 text-xs space-y-2"
              >
                <div className="flex items-center justify-between text-rose-600 dark:text-rose-400 font-semibold">
                  <span>{t(($) => $.office.inspector.escalation_with_category, { category: esc.category })}</span>
                  <Badge variant="destructive" className="text-[9px] h-3.5 px-1 uppercase">
                    {esc.severity}
                  </Badge>
                </div>
                <p className="text-foreground text-[11.5px]">{esc.summary}</p>
                {canControl && onResolveEscalation && (
                  <Button
                    type="button"
                    size="xs"
                    onClick={() => onResolveEscalation(esc.id)}
                    className="w-full gap-1 text-xs"
                  >
                    <Check className="size-3" />
                    <span>{t(($) => $.office.inspector.resolve_escalation)}</span>
                  </Button>
                )}
              </div>
            ))}

            {scene.pendingApprovalsCount === 0 && !selectedDesk && (
              <div className="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground space-y-1">
                <CheckCircle2 className="mx-auto size-5 text-emerald-500" />
                <p className="font-medium text-foreground">{t(($) => $.office.inspector.all_clear)}</p>
                <p className="text-[11px]">{t(($) => $.office.inspector.no_pending)}</p>
              </div>
            )}
          </div>
        </div>
      )}
    </aside>
  );
}
