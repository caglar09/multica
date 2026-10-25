/* eslint-disable i18next/no-literal-string */
"use client";

import { useMemo } from "react";
import { Workflow, CheckCircle2, LoaderCircle, CircleDot, ArrowRight } from "lucide-react";

import type { AutonomousProjectSnapshot, AutonomousActivityItem } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Badge } from "@multica/ui/components/ui/badge";

import { useT } from "../../../../i18n";

export interface ExecutionLoopWidgetProps {
  snapshot?: AutonomousProjectSnapshot | null;
  onViewActivity?: () => void;
  className?: string;
}

function formatRelative(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  const diff = Date.now() - date.getTime();
  if (!Number.isFinite(diff)) return value;
  const minutes = Math.round(diff / 60000);
  if (Math.abs(minutes) < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

function isRunningActivity(item: AutonomousActivityItem): boolean {
  return item.type.endsWith(".running") || item.type.endsWith(".dispatched");
}

export function ExecutionLoopWidget({
  snapshot,
  onViewActivity,
  className,
}: ExecutionLoopWidgetProps) {
  const { t } = useT("projects");

  const activities = useMemo(
    () => snapshot?.activity ?? [],
    [snapshot?.activity],
  );

  const liveActivity = useMemo(
    () => activities.find(isRunningActivity),
    [activities],
  );

  const hasDraft = Boolean(snapshot?.draft);
  const teamConfirmed = Boolean(
    (activities.some((item) => item.type === "team.confirmed") || snapshot?.team) && !hasDraft,
  );
  const teamActive = hasDraft || (!teamConfirmed && Boolean(snapshot?.bootstrap));
  const planCreated = Boolean(
    snapshot?.plan?.nodes && snapshot.plan.nodes.length > 0,
  );
  const executionStarted = Boolean(
    (snapshot?.workflows && snapshot.workflows.length > 0) || liveActivity,
  );
  const allCompleted = Boolean(
    planCreated &&
      snapshot?.plan?.nodes.every((n) => n.status === "completed" || n.status === "done"),
  );

  const stages = [
    { label: t(($) => $.cockpit.stage_brief), completed: Boolean(snapshot?.bootstrap) },
    { label: t(($) => $.cockpit.stage_team), completed: teamConfirmed, active: teamActive },
    { label: t(($) => $.cockpit.stage_plan), completed: planCreated, active: teamConfirmed && !planCreated },
    { label: t(($) => $.cockpit.stage_exec), completed: executionStarted, active: executionStarted && !allCompleted },
    { label: t(($) => $.cockpit.stage_done), completed: allCompleted },
  ];

  return (
    <div
      className={cn(
        "flex flex-col rounded-xl border bg-card p-4 shadow-xs transition-colors",
        className,
      )}
    >
      {/* Header */}
      <div className="flex items-center justify-between gap-2 pb-3">
        <div className="flex items-center gap-2">
          <div className="flex size-7 items-center justify-center rounded-lg bg-emerald-500/10 text-emerald-600 dark:text-emerald-400">
            <Workflow className="size-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold leading-none text-foreground">
              {t(($) => $.cockpit.execution_loop_title)}
            </h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t(($) => $.cockpit.execution_loop_desc)}
            </p>
          </div>
        </div>

        {liveActivity ? (
          <Badge variant="default" className="gap-1.5 bg-emerald-600 text-white dark:bg-emerald-500 text-[10px]">
            <LoaderCircle className="size-3 animate-spin" />
            Live Execution
          </Badge>
        ) : snapshot?.control.paused ? (
          <Badge variant="secondary" className="gap-1.5 text-[10px]">
            <span className="size-1.5 rounded-full bg-amber-500" />
            Paused
          </Badge>
        ) : (
          <Badge variant="outline" className="gap-1.5 text-[10px]">
            <span className="size-1.5 rounded-full bg-muted-foreground/50" />
            Standing By
          </Badge>
        )}
      </div>

      {/* Stage Flow Stepper */}
      <div className="py-2">
        <div className="grid grid-cols-5 gap-1.5 text-center">
          {stages.map((stage, idx) => {
            const isCurrent = stage.active || (stage.completed && !stages[idx + 1]?.completed);
            return (
              <div
                key={stage.label}
                className={cn(
                  "flex flex-col items-center rounded-lg border px-2 py-2 transition-all",
                  stage.completed
                    ? "border-emerald-500/30 bg-emerald-500/5 text-foreground"
                    : isCurrent
                      ? "border-primary/50 bg-primary/5 text-foreground ring-1 ring-primary/20"
                      : "border-border/40 bg-muted/20 text-muted-foreground opacity-60",
                )}
              >
                <div className="flex items-center justify-center mb-1">
                  {stage.completed ? (
                    <CheckCircle2 className="size-3.5 text-emerald-500" />
                  ) : isCurrent ? (
                    <span className="relative flex size-3">
                      <span className="absolute inline-flex size-full animate-ping rounded-full bg-primary/70" />
                      <span className="relative inline-flex size-3 rounded-full bg-primary" />
                    </span>
                  ) : (
                    <span className="size-2.5 rounded-full border border-muted-foreground/40" />
                  )}
                </div>
                <span className="text-[10px] font-medium leading-tight">
                  {stage.label}
                </span>
              </div>
            );
          })}
        </div>
      </div>

      {/* Current Active Loop Card or Live Activity */}
      <div className="mt-3 flex-1 flex flex-col justify-between">
        {liveActivity ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3">
            <div className="flex items-center justify-between gap-2">
              <div className="flex items-center gap-2 font-medium text-xs text-emerald-700 dark:text-emerald-300">
                <LoaderCircle className="size-3.5 animate-spin shrink-0" />
                <span className="truncate">{liveActivity.title}</span>
              </div>
              <span className="text-[10px] font-mono text-emerald-600/70 shrink-0">
                {formatRelative(liveActivity.created_at)}
              </span>
            </div>
            {liveActivity.detail && (
              <p className="mt-1.5 text-[11px] text-muted-foreground line-clamp-2">
                {liveActivity.detail}
              </p>
            )}
          </div>
        ) : (
          <div className="rounded-lg border border-border/40 bg-muted/10 p-3">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <CircleDot className="size-3.5 shrink-0" />
              <span>{t(($) => $.cockpit.loop_no_active)}</span>
            </div>
          </div>
        )}

        {/* Recent Events Micro Feed */}
        <div className="mt-3 space-y-1.5">
          <div className="flex items-center justify-between text-[10px] text-muted-foreground px-1">
            <span className="uppercase font-semibold tracking-wider font-mono">
              Recent Stream
            </span>
            {onViewActivity && (
              <button
                type="button"
                onClick={onViewActivity}
                className="flex items-center gap-0.5 text-primary hover:underline cursor-pointer"
              >
                <span>Full Feed</span>
                <ArrowRight className="size-2.5" />
              </button>
            )}
          </div>

          <div className="space-y-1">
            {activities.slice(0, 3).map((item) => (
              <div
                key={item.id}
                className="flex items-center justify-between gap-2 rounded-md bg-muted/20 px-2.5 py-1.5 text-[11px] border border-border/30"
              >
                <div className="flex items-center gap-2 min-w-0">
                  <span
                    className={cn(
                      "size-1.5 rounded-full shrink-0",
                      isRunningActivity(item)
                        ? "bg-emerald-500 animate-pulse"
                        : "bg-muted-foreground/40",
                    )}
                  />
                  <span className="truncate font-medium text-foreground">
                    {item.title}
                  </span>
                </div>
                <span className="text-[10px] font-mono text-muted-foreground shrink-0">
                  {formatRelative(item.created_at)}
                </span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
