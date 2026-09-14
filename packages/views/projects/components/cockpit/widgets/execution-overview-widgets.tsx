"use client";

import { Activity, AlertTriangle, ChevronDown, CirclePause, Clock3, RotateCcw, Workflow } from "lucide-react";

import type {
  AutonomousActivityItem,
  AutonomousDiagnostic,
  AutonomousProjectSnapshot,
  AutonomousWorkflowRun,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";

import { useT } from "../../../../i18n";

type WaitingActions = {
  onRestartWorkflow?: () => void;
  onResumeExecution?: () => void;
  onReplan?: () => void;
  onRetryAction?: (actionId: string) => void;
  onRerunIssue?: (issueId: string, taskId?: string) => void;
  pending?: boolean;
};

export function WorkWaitingWidget({
  snapshot,
  canControl = false,
  mode = "all",
  scrollable = true,
  idPrefix = "work-waiting",
  ...actions
}: {
  snapshot?: AutonomousProjectSnapshot | null;
  canControl?: boolean;
  mode?: "all" | "urgent" | "nonurgent";
  scrollable?: boolean;
  idPrefix?: string;
} & WaitingActions) {
  const { t } = useT("projects");
  const diagnostics = snapshot?.diagnostics ?? [];
  const visibleDiagnostics = diagnostics.filter((item) =>
    mode === "urgent"
      ? item.severity === "error"
      : mode === "nonurgent"
        ? item.severity !== "error"
        : true,
  );
  const paused = Boolean(snapshot?.control.paused);
  const lastError = snapshot?.control.last_error;
  const scheduledWork = mode === "nonurgent" ? [] : scheduledRetries(snapshot);
  const plannedNodes = (snapshot?.plan?.nodes ?? []).filter((node) =>
    ["pending", "ready", "running", "verification", "blocked"].includes(node.status),
  );
  const showPlannedWork =
    mode !== "urgent" &&
    plannedNodes.length > 0 &&
    canControl &&
    Boolean(actions.onRestartWorkflow);
  const diagnosticGroups: Array<{
    severity: "error" | "warning" | "info";
    label: string;
    items: AutonomousDiagnostic[];
  }> = [
    { severity: "error", label: t(($) => $.cockpit.waiting_errors), items: visibleDiagnostics.filter((item) => item.severity === "error") },
    { severity: "warning", label: t(($) => $.cockpit.waiting_warnings), items: visibleDiagnostics.filter((item) => item.severity === "warning") },
    { severity: "info", label: t(($) => $.cockpit.waiting_info), items: visibleDiagnostics.filter((item) => !["error", "warning"].includes(item.severity)) },
  ];

  const showPause = mode !== "nonurgent" && paused;
  const showLastError = mode !== "nonurgent" && Boolean(lastError);
  if (!showPause && !showLastError && visibleDiagnostics.length === 0 && scheduledWork.length === 0 && !showPlannedWork) return null;

  return (
    <details
      open
      className="group rounded-xl border border-amber-500/40 bg-amber-500/[0.04] shadow-xs"
      aria-labelledby={`${idPrefix}-title`}
    >
      <summary className="flex cursor-pointer list-none items-start justify-between gap-2 border-b border-amber-500/20 p-4 [&::-webkit-details-marker]:hidden">
        <div className="flex gap-2.5">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <AlertTriangle className="size-4" />
          </div>
          <div>
            <h2 id={`${idPrefix}-title`} className="text-sm font-semibold text-foreground">
              {t(($) => $.cockpit.waiting_title)}
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.cockpit.waiting_description)}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Badge variant="secondary" className="self-start font-mono text-[10px]">
            {visibleDiagnostics.length + Number(showPause) + Number(showLastError)} {t(($) => $.cockpit.waiting_attention_count)}
          </Badge>
          <ChevronDown className="mt-1 size-4 text-muted-foreground transition-transform group-open:rotate-180" aria-hidden="true" />
        </div>
      </summary>

      <div className={cn("space-y-3 p-4 pt-3", scrollable && "max-h-[32rem] overflow-y-auto")} aria-labelledby={`${idPrefix}-title`}>
        {showPlannedWork ? (
          <div className="flex flex-col gap-3 rounded-lg border border-primary/25 bg-primary/[0.03] p-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 gap-2.5">
              <Workflow className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
              <div>
                <div className="text-sm font-medium text-foreground">
                  {t(($) => $.cockpit.planned_title)}
                </div>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {t(($) => $.cockpit.planned_description, { count: plannedNodes.length })}
                </p>
              </div>
            </div>
            <Button size="sm" onClick={actions.onRestartWorkflow} disabled={actions.pending}>
              <RotateCcw className={cn("size-3.5", actions.pending && "animate-spin")} />
              {t(($) => $.cockpit.planned_run_now)}
            </Button>
          </div>
        ) : null}
        {scheduledWork.length > 0 ? (
          <ScheduledWork
            items={scheduledWork}
            plannedCount={(snapshot?.plan?.nodes ?? []).filter((node) =>
              ["pending", "blocked"].includes(node.status),
            ).length}
            canControl={canControl}
            pending={actions.pending}
            onRerunIssue={actions.onRerunIssue}
          />
        ) : null}
        {showPause ? (
          <div className="flex flex-col gap-3 rounded-lg border border-amber-500/30 bg-background/70 p-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 gap-2.5">
              <CirclePause className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
              <div>
                <div className="text-sm font-medium text-foreground">{t(($) => $.cockpit.waiting_paused_title)}</div>
                <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.cockpit.waiting_paused_description)}</p>
              </div>
            </div>
            {canControl && actions.onResumeExecution ? (
              <Button size="sm" onClick={actions.onResumeExecution} disabled={actions.pending}>
                <RotateCcw className={cn("size-3.5", actions.pending && "animate-spin")} />
                {t(($) => $.cockpit.resume_execution)}
              </Button>
            ) : null}
          </div>
        ) : null}

        {showLastError ? (
          <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-3">
            <div className="text-xs font-medium text-destructive">{t(($) => $.cockpit.waiting_control_error)}</div>
            <p className="mt-1 break-words text-xs text-muted-foreground">{lastError}</p>
          </div>
        ) : null}

        {diagnosticGroups.map(({ severity, label, items }) => items.length > 0 ? (
          <section key={severity} className="space-y-2" aria-labelledby={`${idPrefix}-${severity}`}>
            <div className="flex items-center justify-between gap-2 px-1">
              <h3 id={`${idPrefix}-${severity}`} className={cn("text-xs font-semibold", severity === "error" ? "text-destructive" : severity === "warning" ? "text-amber-700 dark:text-amber-400" : "text-muted-foreground")}>
                {label}
              </h3>
              <Badge variant={severity === "error" ? "destructive" : "secondary"} className="font-mono text-[10px]">{items.length}</Badge>
            </div>
            <div className="space-y-2">
              {items.map((diagnostic) => (
                <WaitingDiagnostic
                  key={[diagnostic.code, diagnostic.node_key ?? "", diagnostic.updated_at].join(":")}
                  diagnostic={diagnostic}
                  canControl={canControl}
                  {...actions}
                />
              ))}
            </div>
          </section>
        ) : null)}
      </div>
    </details>
  );
}

type ScheduledRetry = {
  id: string;
  issueId: string;
  taskId: string;
  title: string;
  detail?: string;
  fireAt?: string;
};

function metadataString(item: AutonomousActivityItem, key: string): string | undefined {
  const value = item.metadata?.[key];
  return typeof value === "string" ? value : undefined;
}

function scheduledRetries(snapshot?: AutonomousProjectSnapshot | null): ScheduledRetry[] {
  return (snapshot?.activity ?? [])
    .filter((item) => item.type === "task.deferred" && Boolean(item.issue_id))
    .flatMap((item) => {
      const taskId = metadataString(item, "task_id");
      if (!taskId || !item.issue_id) return [];
      return [{
        id: item.id,
        issueId: item.issue_id,
        taskId,
        title: item.title,
        detail: item.detail,
        fireAt: metadataString(item, "fire_at"),
      }];
    })
    .sort((left, right) => (left.fireAt ?? "").localeCompare(right.fireAt ?? ""));
}

function ScheduledWork({
  items,
  plannedCount,
  canControl,
  pending,
  onRerunIssue,
}: {
  items: ScheduledRetry[];
  plannedCount: number;
  canControl: boolean;
  pending?: boolean;
  onRerunIssue?: (issueId: string, taskId?: string) => void;
}) {
  const { t } = useT("projects");
  return (
    <div className="rounded-lg border border-primary/25 bg-primary/[0.03] p-3">
      <div className="flex gap-2.5">
        <Clock3 className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden="true" />
        <div className="min-w-0">
          <div className="text-sm font-medium text-foreground">{t(($) => $.cockpit.scheduled_title)}</div>
          <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.cockpit.scheduled_description)}</p>
        </div>
      </div>
      <div className="mt-3 space-y-2">
        {items.map((item) => (
          <div key={item.id} className="flex flex-col gap-3 rounded-md border bg-background/70 p-2.5 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <div className="truncate text-xs font-medium text-foreground">{item.title}</div>
              {item.detail ? <p className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{item.detail}</p> : null}
              <p className="mt-1 text-[11px] text-muted-foreground">
                {item.fireAt ? t(($) => $.cockpit.scheduled_retry_at, { time: formatTime(item.fireAt) }) : t(($) => $.cockpit.scheduled_queued)}
                {" · "}
                {t(($) => $.cockpit.scheduled_runtime_note)}
              </p>
            </div>
            {canControl && onRerunIssue ? (
              <Button size="sm" onClick={() => onRerunIssue(item.issueId, item.taskId)} disabled={pending}>
                <RotateCcw className={cn("size-3.5", pending && "animate-spin")} />
                {t(($) => $.cockpit.scheduled_run_now)}
              </Button>
            ) : null}
          </div>
        ))}
      </div>
      {plannedCount > items.length ? (
        <p className="mt-3 text-[11px] text-muted-foreground">
          {t(($) => $.cockpit.scheduled_other_planned, { count: plannedCount - items.length })}
        </p>
      ) : null}
    </div>
  );
}

function WaitingDiagnostic({
  diagnostic,
  canControl,
  onRestartWorkflow,
  onResumeExecution,
  onReplan,
  onRetryAction,
  onRerunIssue,
  pending,
}: { diagnostic: AutonomousDiagnostic; canControl: boolean } & WaitingActions) {
  const { t } = useT("projects");
  const action = diagnostic.resume_action;
  const actionLabel =
    action === "resume_project"
      ? t(($) => $.cockpit.resume_execution)
      : action === "replan"
        ? t(($) => $.cockpit.replan_project)
        : action === "retry_action"
          ? t(($) => $.cockpit.waiting_retry_action)
            : action === "rerun_issue"
              ? t(($) => $.cockpit.waiting_rerun_task)
            : action === "restart_workflow"
              ? t(($) => $.cockpit.waiting_recovery_check)
              : null;
  const runAction = () => {
    switch (action) {
      case "resume_project": onResumeExecution?.(); break;
      case "replan": onReplan?.(); break;
      case "retry_action": if (diagnostic.action_id) onRetryAction?.(diagnostic.action_id); break;
      case "rerun_issue": if (diagnostic.issue_id) onRerunIssue?.(diagnostic.issue_id, diagnostic.task_id); break;
      case "restart_workflow": onRestartWorkflow?.(); break;
    }
  };
  const actionable = canControl && diagnostic.can_resume && Boolean(actionLabel) &&
    ((action === "resume_project" && onResumeExecution) ||
      (action === "replan" && onReplan) ||
      (action === "retry_action" && diagnostic.action_id && onRetryAction) ||
      (action === "rerun_issue" && diagnostic.issue_id && onRerunIssue) ||
      (action === "restart_workflow" && onRestartWorkflow));

  return (
    <div className="flex flex-col gap-3 rounded-lg border bg-background/70 p-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={diagnostic.severity === "error" ? "destructive" : "secondary"}>{diagnostic.severity}</Badge>
          <span className="text-sm font-medium text-foreground">{diagnostic.title}</span>
          <span className="font-mono text-[10px] text-muted-foreground">{diagnostic.code}</span>
        </div>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{diagnostic.detail}</p>
        {diagnostic.issue_title || diagnostic.node_key ? (
          <p className="mt-1.5 text-[11px] text-muted-foreground">
            {[diagnostic.node_key, diagnostic.issue_title].filter(Boolean).join(" · ")}
          </p>
        ) : null}
      </div>
      {actionable && actionLabel ? (
        <Button variant={diagnostic.severity === "error" ? "default" : "outline"} size="sm" disabled={pending} onClick={runAction}>
          <RotateCcw className={cn("size-3.5", pending && "animate-spin")} />
          {actionLabel}
        </Button>
      ) : action === "resolve_escalation" ? (
        <Badge variant="outline">{t(($) => $.cockpit.waiting_resolve_in_decisions)}</Badge>
      ) : null}
    </div>
  );
}

export function WorkflowQueueWidget({ snapshot, className }: { snapshot?: AutonomousProjectSnapshot | null; className?: string }) {
  const { t } = useT("projects");
  const workflows = snapshot?.workflows ?? [];
  const active = workflows.filter((run) => !["done", "blocked", "in_review"].includes(run.state));
  const blocked = workflows.filter((run) => run.state === "blocked");
  const review = workflows.filter((run) => run.state === "in_review");
  const visible = [...blocked, ...active, ...review].slice(0, 4);
  const remaining = workflows.filter((run) => !visible.some((item) => item.id === run.id));

  return (
    <section className={cn("flex flex-col rounded-xl border bg-card p-4 shadow-xs", className)} aria-labelledby="workflow-queue-title">
      <div className="flex items-start gap-2.5 pb-3">
        <div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary"><Workflow className="size-4" /></div>
        <div>
          <h2 id="workflow-queue-title" className="text-sm font-semibold text-foreground">{t(($) => $.cockpit.workflow_title)}</h2>
          <p className="mt-1 text-[11px] text-muted-foreground">{t(($) => $.cockpit.workflow_description)}</p>
        </div>
      </div>
      <div className="grid grid-cols-3 gap-2 border-y py-3 text-center text-xs">
        <Metric label={t(($) => $.cockpit.workflow_active)} value={active.length} />
        <Metric label={t(($) => $.cockpit.workflow_blocked)} value={blocked.length} warn={blocked.length > 0} />
        <Metric label={t(($) => $.cockpit.workflow_review)} value={review.length} />
      </div>
      {visible.length > 0 ? <div className="mt-3 space-y-2">{visible.map((run) => <WorkflowRow key={run.id} run={run} />)}</div> : <p className="py-6 text-center text-xs text-muted-foreground">{t(($) => $.cockpit.workflow_empty)}</p>}
      {remaining.length > 0 ? <details className="mt-3 border-t pt-3"><summary className="cursor-pointer text-xs font-medium text-primary">{t(($) => $.cockpit.workflow_show_all, { count: remaining.length })}</summary><div className="mt-2 space-y-2">{remaining.map((run) => <WorkflowRow key={run.id} run={run} />)}</div></details> : null}
    </section>
  );
}

function Metric({ label, value, warn = false }: { label: string; value: number; warn?: boolean }) {
  return <div><div className={cn("font-mono text-base font-semibold", warn && "text-destructive")}>{value}</div><div className="mt-0.5 text-[10px] text-muted-foreground">{label}</div></div>;
}

function WorkflowRow({ run }: { run: AutonomousWorkflowRun }) {
  return <div className="rounded-lg border bg-muted/20 p-2.5"><div className="flex items-start justify-between gap-2"><span className="min-w-0 truncate text-xs font-medium text-foreground">{run.issue_title}</span><Badge variant={run.state === "blocked" ? "destructive" : "outline"} className="shrink-0 text-[10px]">{run.state}</Badge></div><p className="mt-1 text-[11px] text-muted-foreground">{run.owner_agent_name ?? "Unassigned"} · {run.pending_actions} pending · {run.failed_actions} failed</p></div>;
}

export function ActivityTimelineWidget({ snapshot, className }: { snapshot?: AutonomousProjectSnapshot | null; className?: string }) {
  const { t } = useT("projects");
  const activity = snapshot?.activity ?? [];
  const visible = activity.slice(0, 4);
  const remaining = activity.slice(4);
  return (
    <section className={cn("flex flex-col rounded-xl border bg-card p-4 shadow-xs", className)} aria-labelledby="activity-timeline-title">
      <div className="flex items-start gap-2.5 pb-3"><div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary"><Activity className="size-4" /></div><div><h2 id="activity-timeline-title" className="text-sm font-semibold text-foreground">{t(($) => $.cockpit.activity_title)}</h2><p className="mt-1 text-[11px] text-muted-foreground">{t(($) => $.cockpit.activity_description)}</p></div></div>
      {visible.length > 0 ? <div className="divide-y">{visible.map((item) => <ActivityRow key={item.id} item={item} />)}</div> : <p className="py-6 text-center text-xs text-muted-foreground">{t(($) => $.cockpit.activity_empty)}</p>}
      {remaining.length > 0 ? <details className="mt-3 border-t pt-3"><summary className="cursor-pointer text-xs font-medium text-primary">{t(($) => $.cockpit.activity_show_all, { count: remaining.length })}</summary><div className="mt-2 divide-y">{remaining.map((item) => <ActivityRow key={item.id} item={item} />)}</div></details> : null}
    </section>
  );
}

function ActivityRow({ item }: { item: AutonomousActivityItem }) {
  return <div className="py-2.5"><div className="flex items-start justify-between gap-3"><div className="min-w-0"><p className="truncate text-xs font-medium text-foreground">{item.title}</p>{item.detail ? <p className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{item.detail}</p> : null}</div><time className="shrink-0 text-[10px] text-muted-foreground">{relativeTime(item.created_at)}</time></div></div>;
}

function relativeTime(value: string) {
  const minutes = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 60_000));
  if (minutes < 1) return "now";
  if (minutes < 60) return `${minutes}m`;
  if (minutes < 1_440) return `${Math.round(minutes / 60)}h`;
  return `${Math.round(minutes / 1_440)}d`;
}

function formatTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}
