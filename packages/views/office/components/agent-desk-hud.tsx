import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { Badge } from "@multica/ui/components/ui/badge";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { Play, Clock, CheckCircle2, ListOrdered } from "lucide-react";
import { useT } from "../../i18n";
import type { OfficeAgentDesk, AgentDeskStatus } from "../types";

interface AgentDeskHudProps {
  desk: OfficeAgentDesk;
  isSelected?: boolean;
  onSelect?: (desk: OfficeAgentDesk) => void;
  className?: string;
}

export function AgentDeskHud({
  desk,
  isSelected,
  onSelect,
  className,
}: AgentDeskHudProps) {
  const { t } = useT("projects");
  const statusCfg = getStatusConfig(desk.status);
  const statusLabel = desk.status === "running"
    ? t(($) => $.office.desk_status.coding)
    : desk.status === "debugging"
      ? t(($) => $.office.desk_status.debugging)
      : desk.status === "verifying"
        ? t(($) => $.office.desk_status.testing)
        : desk.status === "blocked"
          ? t(($) => $.office.desk_status.blocked)
          : desk.status === "approved"
            ? t(($) => $.office.desk_status.approved)
            : t(($) => $.office.desk_status.standby);

  const activeTask = desk.activeTask;
  const queuedTasks = desk.queuedTasks ?? [];
  const completedCount = desk.completedTasksCount ?? 0;
  const hasActive = Boolean(activeTask);
  const totalTasks = desk.totalAssignedTasksCount || (hasActive ? 1 : 0) + queuedTasks.length + completedCount;

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => onSelect?.(desk)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect?.(desk);
        }
      }}
      className={cn(
        "group relative flex w-56 cursor-pointer flex-col gap-2 rounded-xl border border-slate-700/80 bg-slate-900/95 p-2.5 shadow-xl backdrop-blur-md transition-all duration-200 select-none",
        "hover:scale-105 hover:border-primary/80 hover:shadow-2xl hover:shadow-primary/20",
        isSelected && "border-primary ring-2 ring-primary/60 ring-offset-2 ring-offset-slate-950",
        className,
      )}
    >
      {/* Top Header: Avatar + Name + Status Dot */}
      <div className="flex items-center gap-2">
        <div className="relative shrink-0">
          <ActorAvatar
            name={desk.agentName}
            initials={desk.agentName.slice(0, 2).toUpperCase()}
            avatarUrl={resolvePublicFileUrl(desk.avatarUrl)}
            size="sm"
            className="size-7 border border-slate-700"
          />
          <span
            className={cn(
              "absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full ring-2 ring-slate-900",
              statusCfg.dotColor,
              desk.status === "running" && "animate-pulse",
            )}
          />
        </div>

        <div className="min-w-0 flex-1">
          <div
            className="truncate text-xs font-semibold leading-tight text-slate-100 group-hover:text-primary transition-colors"
            title={desk.agentName}
          >
            {desk.agentName}
          </div>
          <div
            className="truncate text-[10.5px] font-mono text-slate-400 leading-tight"
            title={desk.role}
          >
            {desk.role}
          </div>
        </div>
      </div>

      {/* Active Task Section */}
      <div className="flex flex-col gap-1.5 rounded-lg bg-slate-800/80 p-2 text-xs border border-slate-700/40">
        <div className="flex items-center justify-between gap-1.5">
          <Badge
            variant="outline"
            className={cn(
              "h-4 px-1.5 text-[9.5px] font-medium border-0 gap-1",
              statusCfg.badgeClass,
            )}
          >
            {hasActive && <Play className="size-2 fill-current" />}
            {statusLabel}
          </Badge>

          {desk.currentIssueId && (
            <span className="font-mono text-[9.5px] text-slate-400">
              {desk.currentIssueId.slice(0, 8)}
            </span>
          )}
        </div>

        {/* Current / Active Task Title */}
        <div className="min-h-[28px] flex items-center">
          <p
            className="line-clamp-2 text-[11px] font-medium text-slate-200 transition-colors group-hover:text-white"
            title={activeTask?.title || desk.currentTaskTitle}
          >
            {activeTask?.title || desk.currentTaskTitle || t(($) => $.office.desk_status.standby_tasks)}
          </p>
        </div>
      </div>

      {/* Task Distribution / Workload Queue ("1 ini yapıyor, 3'ü beklemede") */}
      {totalTasks > 0 && (
        <div className="flex flex-col gap-1 pt-0.5 border-t border-slate-800">
          <div className="flex items-center justify-between text-[10px]">
            {/* Active vs Queued Ratio */}
            <div className="flex items-center gap-1 font-medium text-slate-300">
              <span className="text-cyan-400">{hasActive ? "1" : "0"} {t(($) => $.office.active_task)}</span>
              {queuedTasks.length > 0 && (
                <>
                  <span className="text-slate-600">•</span>
                  <span className="text-amber-400">{t(($) => $.office.queued_tasks, { count: queuedTasks.length })}</span>
                </>
              )}
            </div>

            {completedCount > 0 && (
              <span className="flex items-center gap-0.5 text-emerald-400 text-[9.5px]">
                <CheckCircle2 className="size-2.5" />
                <span>{completedCount}</span>
              </span>
            )}
          </div>

          {/* Queued Tasks Preview with Tooltip */}
          {queuedTasks.length > 0 && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <div className="flex items-center gap-1 rounded bg-slate-800/40 px-1.5 py-0.5 text-[9.5px] text-slate-400 hover:text-slate-200 transition-colors">
                    <Clock className="size-2.5 text-amber-400 shrink-0" />
                    <span className="truncate flex-1 text-left">
                      {t(($) => $.office.next_in_queue, { title: queuedTasks[0]?.title ?? "" })}
                    </span>
                    {queuedTasks.length > 1 && (
                      <span className="shrink-0 font-mono text-[9px] text-amber-400/90 font-medium">
                        {t(($) => $.office.more_queued, { count: queuedTasks.length - 1 })}
                      </span>
                    )}
                  </div>
                }
              />
              <TooltipContent side="bottom" align="start" className="max-w-xs space-y-1.5 p-2 bg-slate-900 border-slate-700">
                <div className="flex items-center gap-1 text-[11px] font-semibold text-slate-200 border-b border-slate-800 pb-1">
                  <ListOrdered className="size-3 text-amber-400" />
                  <span>{t(($) => $.office.queued_header)} ({queuedTasks.length})</span>
                </div>
                <ul className="space-y-1 text-[10px] text-slate-300">
                  {queuedTasks.map((t, idx) => (
                    <li key={t.id} className="flex items-start gap-1.5">
                      <span className="font-mono text-amber-400 shrink-0">{idx + 1}.</span>
                      <span className="line-clamp-2">{t.title}</span>
                    </li>
                  ))}
                </ul>
              </TooltipContent>
            </Tooltip>
          )}

          {/* Mini Progress Bar representing workload */}
          <div className="flex h-1 w-full gap-0.5 overflow-hidden rounded-full bg-slate-800">
            {hasActive && (
              <div
                style={{ width: `${Math.max((1 / totalTasks) * 100, 20)}%` }}
                className="bg-cyan-500 animate-pulse"
                title={t(($) => $.office.active_task)}
              />
            )}
            {queuedTasks.map((_, i) => (
              <div
                key={i}
                style={{ width: `${Math.max((1 / totalTasks) * 100, 15)}%` }}
                className="bg-amber-500/60"
                title={t(($) => $.office.queued_tasks, { count: i + 1 })}
              />
            ))}
            {completedCount > 0 && (
              <div
                style={{ width: `${Math.max((completedCount / totalTasks) * 100, 15)}%` }}
                className="bg-emerald-500/80"
                title={t(($) => $.office.stats.completed_tasks)}
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function getStatusConfig(status: AgentDeskStatus) {
  switch (status) {
    case "running":
      return {
        label: "coding",
        dotColor: "bg-cyan-400 shadow-xs shadow-cyan-400/50",
        badgeClass: "bg-cyan-500/15 text-cyan-400 border-cyan-500/30",
      };
    case "debugging":
      return {
        label: "debugging",
        dotColor: "bg-amber-400 shadow-xs shadow-amber-400/50",
        badgeClass: "bg-amber-500/15 text-amber-400 border-amber-500/30",
      };
    case "verifying":
      return {
        label: "testing",
        dotColor: "bg-purple-400 shadow-xs shadow-purple-400/50",
        badgeClass: "bg-purple-500/15 text-purple-400 border-purple-500/30",
      };
    case "blocked":
      return {
        label: "blocked",
        dotColor: "bg-rose-400 shadow-xs shadow-rose-400/50",
        badgeClass: "bg-rose-500/15 text-rose-400 border-rose-500/30",
      };
    case "approved":
      return {
        label: "approved",
        dotColor: "bg-emerald-400 shadow-xs shadow-emerald-400/50",
        badgeClass: "bg-emerald-500/15 text-emerald-400 border-emerald-500/30",
      };
    default:
      return {
        label: "standby",
        dotColor: "bg-slate-500",
        badgeClass: "bg-slate-800 text-slate-400 border-slate-700",
      };
  }
}
