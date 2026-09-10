"use client";

import { useMemo } from "react";
import { ListTodo, ArrowRight } from "lucide-react";

import type { Project } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";

import { useT } from "../../../../i18n";
import { getProjectIssueMetrics } from "../../project-issue-metrics";

export interface IssuesVelocityWidgetProps {
  project: Project;
  onViewIssues?: () => void;
  className?: string;
}

export function IssuesVelocityWidget({
  project,
  onViewIssues,
  className,
}: IssuesVelocityWidgetProps) {
  const { t } = useT("projects");

  const metrics = useMemo(
    () => getProjectIssueMetrics(project),
    [project],
  );

  const percent = useMemo(() => {
    if (metrics.totalCount === 0) return 0;
    return Math.round((metrics.completedCount / metrics.totalCount) * 100);
  }, [metrics.completedCount, metrics.totalCount]);

  const remainingCount = Math.max(0, metrics.totalCount - metrics.completedCount);

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
          <div className="flex size-7 items-center justify-center rounded-lg bg-sky-500/10 text-sky-600 dark:text-sky-400">
            <ListTodo className="size-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold leading-none text-foreground">
              {t(($) => $.cockpit.issues_velocity_title)}
            </h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t(($) => $.cockpit.issues_velocity_desc)}
            </p>
          </div>
        </div>

        {onViewIssues && (
          <Button
            variant="ghost"
            size="sm"
            className="gap-1 text-xs text-primary h-7 px-2"
            onClick={onViewIssues}
          >
            <span>{t(($) => $.cockpit.view_all_issues)}</span>
            <ArrowRight className="size-3" />
          </Button>
        )}
      </div>

      {/* Metric Gauge & Progress */}
      <div className="flex-1 flex flex-col justify-between space-y-4">
        <div>
          <div className="flex items-baseline justify-between mb-2">
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold font-mono tracking-tight text-foreground">
                {percent}%
              </span>
              <span className="text-xs text-muted-foreground">
                {t(($) => $.cockpit.tasks_progress_summary, {
                  completed: metrics.completedCount,
                  total: metrics.totalCount,
                })}
              </span>
            </div>
            {metrics.totalCount > 0 && (
              <Badge variant="outline" className="font-mono text-[10px]">
                {t(($) => $.cockpit.tasks_remaining, { count: remainingCount })}
              </Badge>
            )}
          </div>

          {/* Progress Bar */}
          <div className="h-2 w-full overflow-hidden rounded-full bg-muted/60">
            <div
              className="h-full rounded-full bg-primary transition-all duration-500 ease-out"
              style={{ width: `${percent}%` }}
            />
          </div>
        </div>

        {/* Breakdown by Status Grid */}
        <div className="grid grid-cols-2 gap-2 pt-1">
          <div className="rounded-lg bg-muted/20 border border-border/40 p-2.5 text-center">
            <span className="block text-[10px] font-medium text-muted-foreground uppercase tracking-wider">
              {t(($) => $.cockpit.tasks_completed)}
            </span>
            <span className="mt-1 block font-mono text-lg font-bold text-emerald-600 dark:text-emerald-400">
              {metrics.completedCount}
            </span>
          </div>

          <div className="rounded-lg bg-muted/20 border border-border/40 p-2.5 text-center">
            <span className="block text-[10px] font-medium text-muted-foreground uppercase tracking-wider">
              {t(($) => $.cockpit.tasks_pending)}
            </span>
            <span className="mt-1 block font-mono text-lg font-bold text-muted-foreground">
              {remainingCount}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
