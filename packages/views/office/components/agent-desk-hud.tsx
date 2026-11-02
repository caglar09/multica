import { memo } from "react";
import { cn } from "@multica/ui/lib/utils";
import { Badge } from "@multica/ui/components/ui/badge";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import {
  Play,
  Clock,
  CheckCircle2,
  ListOrdered,
  Terminal,
  Zap,
  Coffee,
  Monitor,
  MessageSquare,
  Moon,
  Crown,
  Sparkles,
} from "lucide-react";
import { useT } from "../../i18n";
import type { OfficeAgentDesk, OfficeBlocker, AgentDeskStatus, AgentCharacterType } from "../types";

interface AgentDeskHudProps {
  desk: OfficeAgentDesk;
  blockers?: OfficeBlocker[];
  isSelected?: boolean;
  onSelect?: (desk: OfficeAgentDesk) => void;
  className?: string;
}

export const AgentDeskHud = memo(function AgentDeskHud({
  desk,
  blockers = [],
  isSelected,
  onSelect,
  className,
}: AgentDeskHudProps) {
  const { t } = useT("projects");
  const statusCfg = getStatusConfig(desk.status);
  const statusLabel = (() => {
    switch (desk.status) {
      case "idle": return t(($) => $.office.desk_status.standby);
      case "running": return t(($) => $.office.desk_status.coding);
      case "debugging": return t(($) => $.office.desk_status.debugging);
      case "verifying": return t(($) => $.office.desk_status.testing);
      case "blocked": return t(($) => $.office.desk_status.blocked);
      case "failed": return t(($) => $.office.desk_status.failed);
      case "approved": return t(($) => $.office.desk_status.approved);
      default: {
        const neverStatus: never = desk.status;
        return neverStatus;
      }
    }
  })();

  const activeTask = desk.activeTask;
  const queuedTasks = desk.queuedTasks ?? [];
  const completedCount = desk.completedTasksCount ?? 0;
  const hasActive = Boolean(activeTask);
  const totalTasks =
    desk.totalAssignedTasksCount ||
    (hasActive ? 1 : 0) + queuedTasks.length + completedCount;

  const isWorking = desk.status === "running" || desk.status === "debugging";
  const activeTool = desk.activeTool;
  const activeSkill = desk.activeSkill;
  const bubble = desk.activeSpeechBubble;
  const furniture = desk.furniture;
  const isLeader = desk.isLeader;
  const category = desk.category;
  const characterType = desk.characterType || (isLeader ? "lion_lead" : "robot_general");

  return (
    <div className="relative flex flex-col items-center">
      {/* 2.5D Isometric Pedestal Base (Beneath the Desk) */}
      <div
        className={cn(
          "absolute -bottom-6 size-28 rounded-full bg-primary/20 blur-md opacity-40 transition-all pointer-events-none motion-reduce:transition-none",
          isLeader && "opacity-60 scale-125",
        )}
        style={{ transform: "rotateX(60deg) scale(1.6)" }}
      />

      {/* Speech Bubble Floating Above the Desk */}
      {bubble && (
        <div
          className={cn(
            "pointer-events-none absolute -top-11 z-30 flex max-w-[260px] items-center gap-1.5 rounded-xl border border-info/40 bg-popover px-2.5 py-1 text-micro font-medium text-popover-foreground shadow-xl transition-all duration-300 animate-in fade-in slide-in-from-bottom-2 motion-reduce:animate-none motion-reduce:transition-none",
            bubble.kind === "qa" && "border-primary/40",
            isLeader && "border-warning/50",
          )}
        >
          {isLeader ? (
            <Crown className="size-3 shrink-0 text-warning" />
          ) : (
            <MessageSquare className="size-3 shrink-0 text-info" />
          )}
          <span className="truncate">{bubble.text}</span>
          <div className="absolute -bottom-1.5 left-1/2 -translate-x-1/2 size-0 border-x-4 border-x-transparent border-t-6 border-t-popover" />
        </div>
      )}

      {/* Main Agent Desk Card (2.5D Workstation Unit) */}
      <div
        role="button"
        tabIndex={0}
        aria-label={t(($) => $.office.select_desk, { name: desk.agentName })}
        aria-pressed={Boolean(isSelected)}
        onClick={() => onSelect?.(desk)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onSelect?.(desk);
          }
        }}
        className={cn(
          "group relative flex w-64 cursor-pointer flex-col gap-2 rounded-2xl border border-border bg-card p-3 text-card-foreground shadow-2xl transition-all duration-200 select-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background motion-reduce:transition-none",
          isLeader
            ? "border-warning/70 ring-2 ring-warning/30 hover:border-warning"
            : "hover:border-primary/60",
          "hover:scale-[1.02] motion-reduce:hover:scale-100",
          isSelected && "ring-2 ring-primary ring-offset-2 ring-offset-background border-primary",
          className,
        )}
      >
        {/* Category Header Badge & Leader Crown */}
        <div className="flex items-center justify-between gap-1 border-b border-border pb-1.5">
          <div className="flex items-center gap-1.5 min-w-0">
            {isLeader ? (
              <Badge
                variant="outline"
                className="h-5 gap-1 border-warning/50 bg-warning/10 px-1.5 text-micro font-bold text-warning"
              >
                <Crown className="size-2.5 fill-warning text-warning" />
                <span>{t(($) => $.office.leader_pm)}</span>
              </Badge>
            ) : category ? (
              <Badge
                variant="outline"
                className="max-w-[170px] truncate border-0 bg-muted px-1.5 text-micro font-semibold text-muted-foreground"
              >
                {t(($) => $.office.categories[category])}
              </Badge>
            ) : null}
          </div>

          {/* Dual monitor & Daemon Live / Token Mode */}
          <div className="flex items-center gap-1 shrink-0">
            {furniture?.dualMonitors && (
              <span className="flex items-center gap-0.5 text-faint-foreground" aria-hidden="true">
                <Monitor className="size-3" />
              </span>
            )}
            {desk.isDaemonActive ? (
              <span
                className="flex items-center gap-1 rounded-md border border-success/40 bg-success/10 px-1.5 py-0.5 font-mono text-micro text-success"
                aria-label={t(($) => $.office.runtime_live)}
              >
                <span className="size-1.5 rounded-full bg-success motion-safe:animate-pulse motion-reduce:animate-none" />
                <span>{t(($) => $.office.runtime_live)}</span>
              </span>
            ) : desk.isDaemonActive === false ? (
              <span
                className="flex items-center gap-1 rounded-md border border-warning/40 bg-warning/10 px-1.5 py-0.5 font-mono text-micro text-warning"
              >
                <Moon className="size-2.5" aria-hidden="true" />
                <span>{t(($) => $.office.runtime_unavailable)}</span>
              </span>
            ) : null}
          </div>
        </div>

        {/* Character Avatar & Desk Info */}
        <div className="flex items-center gap-2.5">
          {/* 2.5D Living Character Avatar */}
          <div className="relative shrink-0">
            <AgentCharacterGraphic
              type={characterType}
              isLeader={isLeader}
              isWorking={isWorking}
            />
            <span
              className={cn(
                "absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full ring-2 ring-card",
                statusCfg.dotColor,
                desk.status === "running" && "motion-safe:animate-pulse motion-reduce:animate-none",
              )}
            />
          </div>

          <div className="min-w-0 flex-1">
            <div
              className="flex items-center gap-1 truncate text-label font-bold leading-tight text-card-foreground transition-colors group-hover:text-primary"
              title={desk.agentName}
            >
              <span className="truncate">{desk.agentName}</span>
              {furniture?.coffeeMug && (
                <span aria-hidden="true">
                  <Coffee className="size-2.5 shrink-0 text-warning" />
                </span>
              )}
            </div>
            <div
              className="mt-0.5 truncate font-mono text-micro leading-tight text-muted-foreground"
              title={desk.role}
            >
              {desk.role}
            </div>
          </div>
        </div>

        {/* Active Task Card */}
        <div className="flex flex-col gap-1 rounded-lg border border-border bg-muted/50 p-2 text-caption">
          <div className="flex items-center justify-between gap-1.5">
            <Badge
              variant="outline"
              className={cn(
                "h-5 gap-1 border-0 px-1.5 text-micro font-medium",
                statusCfg.badgeClass,
              )}
            >
              {hasActive && <Play className="size-2 fill-current" />}
              {statusLabel}
            </Badge>

          </div>

          {/* Current Task Title */}
          <div className="min-h-[26px] flex items-center">
            <p
              className="line-clamp-2 text-micro font-medium text-foreground transition-colors group-hover:text-primary"
              title={activeTask?.title || desk.currentTaskTitle}
            >
              {activeTask?.title || desk.currentTaskTitle || t(($) => $.office.desk_status.standby_tasks)}
            </p>
          </div>
        </div>

        {blockers[0] && (
          <div className={cn(
            "flex min-w-0 items-start gap-1.5 rounded-md border px-2 py-1.5 text-micro",
            blockers[0].severity === "error"
              ? "border-destructive/30 bg-destructive/10 text-destructive"
              : blockers[0].severity === "warning"
                ? "border-warning/30 bg-warning/10 text-warning"
                : "border-info/30 bg-info/10 text-info",
          )}>
            <span className="mt-1 size-1.5 shrink-0 rounded-full bg-current" aria-hidden="true" />
            <span className="line-clamp-2">
              {t(($) => $.office.blockers.reasons[blockers[0]!.kind], {
                dependencyCount: blockers[0]!.params?.dependencyCount ?? 1,
              })}
            </span>
            {blockers.length > 1 && (
              <span className="shrink-0 font-medium">
                {t(($) => $.office.blockers.more_count, { count: blockers.length - 1 })}
              </span>
            )}
          </div>
        )}

        {/* Live Tool Invocation Stream */}
        {activeTool && isWorking && (
          <div className="flex items-center gap-1.5 rounded-md border border-info/30 bg-muted px-2 py-1 font-mono text-micro text-foreground">
            <Terminal className="size-3 shrink-0 text-info motion-safe:animate-pulse motion-reduce:animate-none" />
            <span className="font-semibold text-info">{activeTool.name}</span>
            <span className="truncate text-muted-foreground" title={activeTool.target}>
              {activeTool.target}
            </span>
          </div>
        )}

        {/* Active Role Skill Badge */}
        {activeSkill && (
          <div className="flex items-center gap-1 px-0.5 font-mono text-micro text-muted-foreground">
            <Zap className="size-2.5 shrink-0 text-warning" />
            <span>{t(($) => $.office.active_skill)}:</span>
            <span className="truncate text-warning" title={activeSkill.name}>
              {activeSkill.name}
            </span>
          </div>
        )}

        {/* Task Distribution / Workload Queue */}
        {totalTasks > 0 && (
          <div className="flex flex-col gap-1 border-t border-border pt-1">
            <div className="flex items-center justify-between text-micro">
              <div className="flex items-center gap-1 font-medium text-foreground">
                <span className="text-info">
                  {t(($) => $.office.active_tasks_count, { count: hasActive ? 1 : 0 })}
                </span>
                {queuedTasks.length > 0 && (
                  <>
                    <span className="text-muted-foreground">•</span>
                    <span className="font-semibold text-warning">
                      {t(($) => $.office.queued_tasks, { count: queuedTasks.length })}
                    </span>
                  </>
                )}
              </div>

              {completedCount > 0 && (
                <span className="flex items-center gap-0.5 text-micro text-success">
                  <CheckCircle2 className="size-2.5" />
                  <span>{completedCount}</span>
                </span>
              )}
            </div>

            {/* Queued Tasks Preview */}
            {queuedTasks.length > 0 && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <div className="flex items-center gap-1 rounded bg-muted/50 px-1.5 py-0.5 text-micro text-muted-foreground transition-colors hover:text-foreground">
                      <Clock className="size-2.5 shrink-0 text-warning" />
                      <span className="truncate flex-1 text-left">
                        {t(($) => $.office.next_in_queue, { title: queuedTasks[0]?.title ?? "" })}
                      </span>
                      {queuedTasks.length > 1 && (
                        <span className="shrink-0 font-mono text-micro font-medium text-warning">
                          {t(($) => $.office.more_queued, { count: queuedTasks.length - 1 })}
                        </span>
                      )}
                    </div>
                  }
                />
                <TooltipContent side="bottom" align="start" className="max-w-xs space-y-1.5 p-2">
                  <div className="flex items-center gap-1 border-b border-border pb-1 text-micro font-semibold text-popover-foreground">
                    <ListOrdered className="size-3 text-warning" />
                    <span>{t(($) => $.office.queued_header)} ({queuedTasks.length})</span>
                  </div>
                  <ul className="space-y-1 text-micro text-popover-foreground">
                    {queuedTasks.map((t, idx) => (
                      <li key={t.id} className="flex items-start gap-1.5">
                        <span className="shrink-0 font-mono text-warning">{idx + 1}.</span>
                        <span className="line-clamp-2">{t.title}</span>
                      </li>
                    ))}
                  </ul>
                </TooltipContent>
              </Tooltip>
            )}

            {/* Mini Workload Progress Bar */}
            <div className="flex h-1 w-full gap-0.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
              {hasActive && (
                <div
                  style={{ width: `${Math.max((1 / totalTasks) * 100, 20)}%` }}
                  className="bg-info motion-safe:animate-pulse motion-reduce:animate-none"
                  title={t(($) => $.office.active_task)}
                />
              )}
              {queuedTasks.map((_, i) => (
                <div
                  key={i}
                  style={{ width: `${Math.max((1 / totalTasks) * 100, 15)}%` }}
                  className="bg-warning/60"
                  title={t(($) => $.office.queued_tasks, { count: i + 1 })}
                />
              ))}
              {completedCount > 0 && (
                <div
                  style={{ width: `${Math.max((completedCount / totalTasks) * 100, 15)}%` }}
                  className="bg-success/80"
                  title={t(($) => $.office.stats.completed_tasks)}
                />
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
});

/**
 * 2.5D Living Stylized Character Avatars (Lion, Owl, Cat, Raccoon, Panda, Fox, Robot)
 */
function AgentCharacterGraphic({
  type,
  isLeader,
  isWorking,
}: {
  type: AgentCharacterType;
  isLeader?: boolean;
  isWorking?: boolean;
}) {
  return (
    <div
      className={cn(
        "relative flex size-10 items-center justify-center rounded-xl border border-border bg-muted shadow-md transition-transform motion-reduce:transition-none",
        isWorking && "scale-105 motion-reduce:scale-100",
        isLeader && "border-warning/60 bg-warning/10",
      )}
      aria-hidden="true"
    >
      {type === "lion_lead" ? (
        <svg viewBox="0 0 36 36" className="size-8">
          {/* Lion Crown & Mane */}
          <circle cx="18" cy="18" r="14" fill="#f59e0b" fillOpacity="0.25" stroke="#f59e0b" strokeWidth="1.5" />
          <path d="M12 12 L15 7 L18 10 L21 7 L24 12 Z" fill="#fbbf24" stroke="#d97706" strokeWidth="0.8" />
          {/* Lion Ears */}
          <circle cx="11" cy="14" r="3" fill="#b45309" />
          <circle cx="25" cy="14" r="3" fill="#b45309" />
          {/* Face */}
          <ellipse cx="18" cy="20" rx="9" ry="8" fill="#fde68a" />
          {/* Eyes */}
          <circle cx="15" cy="18" r="1.5" fill="#78350f" />
          <circle cx="21" cy="18" r="1.5" fill="#78350f" />
          {/* Nose & Whiskers */}
          <polygon points="17,21 19,21 18,23" fill="#b45309" />
          <path d="M14 23 L10 22 M14 24 L10 25 M22 23 L26 22 M22 24 L26 25" stroke="#b45309" strokeWidth="0.8" />
        </svg>
      ) : type === "owl_architect" ? (
        <svg viewBox="0 0 36 36" className="size-8">
          {/* Owl Body */}
          <ellipse cx="18" cy="19" rx="13" ry="14" fill="#0284c7" fillOpacity="0.25" stroke="#0ea5e9" strokeWidth="1.5" />
          {/* Owl Feather Tufts */}
          <path d="M10 11 L7 6 L12 8 Z" fill="#0369a1" />
          <path d="M26 11 L29 6 L24 8 Z" fill="#0369a1" />
          {/* Owl Glasses & Big Eyes */}
          <circle cx="14" cy="17" r="4.5" fill="#0c4a6e" stroke="#38bdf8" strokeWidth="1.5" />
          <circle cx="22" cy="17" r="4.5" fill="#0c4a6e" stroke="#38bdf8" strokeWidth="1.5" />
          <line x1="18.5" y1="17" x2="17.5" y2="17" stroke="#38bdf8" strokeWidth="1.5" />
          <circle cx="14" cy="17" r="2" fill="#38bdf8" />
          <circle cx="22" cy="17" r="2" fill="#38bdf8" />
          {/* Beak */}
          <polygon points="17,21 19,21 18,24" fill="#f59e0b" />
        </svg>
      ) : type === "cat_mobile" || type === "cat_frontend" ? (
        <svg viewBox="0 0 36 36" className="size-8">
          {/* Cat Ears */}
          <polygon points="8,15 12,6 16,13" fill="#ec4899" stroke="#db2777" strokeWidth="1" />
          <polygon points="28,15 24,6 20,13" fill="#ec4899" stroke="#db2777" strokeWidth="1" />
          {/* Cat Head */}
          <circle cx="18" cy="20" r="11" fill="#f472b6" fillOpacity="0.2" stroke="#ec4899" strokeWidth="1.5" />
          {/* Eyes */}
          <ellipse cx="14" cy="18" rx="2" ry="2.5" fill="#db2777" />
          <ellipse cx="22" cy="18" rx="2" ry="2.5" fill="#db2777" />
          {/* Nose & Mouth */}
          <polygon points="17,22 19,22 18,23.5" fill="#f43f5e" />
          <path d="M18 23.5 Q16 26 15 25 M18 23.5 Q20 26 21 25" stroke="#f43f5e" strokeWidth="1" fill="none" />
          {/* Whiskers */}
          <line x1="12" y1="21" x2="7" y2="20" stroke="#f472b6" strokeWidth="0.8" />
          <line x1="12" y1="23" x2="7" y2="24" stroke="#f472b6" strokeWidth="0.8" />
          <line x1="24" y1="21" x2="29" y2="20" stroke="#f472b6" strokeWidth="0.8" />
          <line x1="24" y1="23" x2="29" y2="24" stroke="#f472b6" strokeWidth="0.8" />
        </svg>
      ) : type === "raccoon_backend" ? (
        <svg viewBox="0 0 36 36" className="size-8">
          {/* Raccoon Head & Mask */}
          <circle cx="18" cy="19" r="13" fill="#7c3aed" fillOpacity="0.25" stroke="#8b5cf6" strokeWidth="1.5" />
          {/* Ears */}
          <polygon points="9,12 8,5 14,9" fill="#4c1d95" />
          <polygon points="27,12 28,5 22,9" fill="#4c1d95" />
          {/* Bandit Mask */}
          <path d="M8 17 Q18 23 28 17 Q25 22 18 22 Q11 22 8 17 Z" fill="#2e1065" />
          {/* Eyes glowing inside mask */}
          <circle cx="14" cy="18" r="1.8" fill="#a78bfa" />
          <circle cx="22" cy="18" r="1.8" fill="#a78bfa" />
          {/* Snout */}
          <ellipse cx="18" cy="24" rx="4" ry="3" fill="#ddd6fe" />
          <circle cx="18" cy="23.5" r="1.2" fill="#1e1b4b" />
        </svg>
      ) : type === "panda_qa" ? (
        <svg viewBox="0 0 36 36" className="size-8">
          {/* Panda Head */}
          <circle cx="18" cy="20" r="12" fill="#10b981" fillOpacity="0.2" stroke="#10b981" strokeWidth="1.5" />
          {/* Black Ears */}
          <circle cx="10" cy="11" r="3.5" fill="#064e3b" />
          <circle cx="26" cy="11" r="3.5" fill="#064e3b" />
          {/* Black Eye Patches */}
          <ellipse cx="14" cy="18" rx="3" ry="2.2" fill="#064e3b" transform="rotate(-15 14 18)" />
          <ellipse cx="22" cy="18" rx="3" ry="2.2" fill="#064e3b" transform="rotate(15 22 18)" />
          <circle cx="14" cy="18" r="1.2" fill="#a7f3d0" />
          <circle cx="22" cy="18" r="1.2" fill="#a7f3d0" />
          {/* Nose */}
          <ellipse cx="18" cy="23" rx="2" ry="1.4" fill="#064e3b" />
        </svg>
      ) : type === "fox_security" ? (
        <svg viewBox="0 0 36 36" className="size-8">
          {/* Fox Ears */}
          <polygon points="7,14 11,5 17,11" fill="#ef4444" stroke="#b91c1c" strokeWidth="1" />
          <polygon points="29,14 25,5 19,11" fill="#ef4444" stroke="#b91c1c" strokeWidth="1" />
          {/* Fox Face */}
          <polygon points="8,15 28,15 18,29" fill="#f87171" fillOpacity="0.3" stroke="#ef4444" strokeWidth="1.5" />
          {/* Cheeks */}
          <polygon points="8,15 18,29 13,22" fill="#fee2e2" />
          <polygon points="28,15 18,29 23,22" fill="#fee2e2" />
          {/* Eyes */}
          <ellipse cx="14" cy="18" rx="1.5" ry="1" fill="#7f1d1d" transform="rotate(20 14 18)" />
          <ellipse cx="22" cy="18" rx="1.5" ry="1" fill="#7f1d1d" transform="rotate(-20 22 18)" />
          {/* Nose */}
          <circle cx="18" cy="27" r="1.5" fill="#450a0a" />
        </svg>
      ) : (
        /* Robot General AI */
        <svg viewBox="0 0 36 36" className="size-8">
          {/* Antenna */}
          <line x1="18" y1="11" x2="18" y2="7" stroke="#6366f1" strokeWidth="1.5" />
          <circle cx="18" cy="6" r="2" fill="#818cf8" />
          {/* Head Box */}
          <rect x="9" y="11" width="18" height="17" rx="4" fill="#312e81" stroke="#6366f1" strokeWidth="1.5" />
          {/* Screen Visor */}
          <rect x="12" y="14" width="12" height="7" rx="2" fill="#1e1b4b" />
          {/* Glowing Eyes */}
          <circle cx="15" cy="17.5" r="1.5" fill="#38bdf8" />
          <circle cx="21" cy="17.5" r="1.5" fill="#38bdf8" />
          {/* Mouth Grid */}
          <line x1="14" y1="24" x2="22" y2="24" stroke="#818cf8" strokeWidth="1" strokeDasharray="1 1" />
        </svg>
      )}

      {/* Floating Sparkle for Active or Leader */}
      {isLeader && (
        <Sparkles className="absolute -top-1 -right-1 size-3 fill-warning text-warning motion-safe:animate-pulse motion-reduce:animate-none" />
      )}
    </div>
  );
}

function getStatusConfig(status: AgentDeskStatus) {
  switch (status) {
    case "running":
      return { dotColor: "bg-info", badgeClass: "bg-info/10 text-info" };
    case "debugging":
      return { dotColor: "bg-warning", badgeClass: "bg-warning/10 text-warning" };
    case "verifying":
      return { dotColor: "bg-primary", badgeClass: "bg-primary/10 text-primary" };
    case "blocked":
    case "failed":
      return { dotColor: "bg-destructive", badgeClass: "bg-destructive/10 text-destructive" };
    case "approved":
      return { dotColor: "bg-success", badgeClass: "bg-success/10 text-success" };
    case "idle":
      return { dotColor: "bg-muted-foreground", badgeClass: "bg-muted text-muted-foreground" };
    default: {
      const neverStatus: never = status;
      return neverStatus;
    }
  }
}
