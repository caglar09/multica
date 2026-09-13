"use client";

import { Activity, AlertTriangle, CirclePause, RotateCcw, Workflow } from "lucide-react";

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
  ...actions
}: { snapshot?: AutonomousProjectSnapshot | null; canControl?: boolean } & WaitingActions) {
  const { t } = useT("projects");
  const diagnostics = snapshot?.diagnostics ?? [];
  const paused = Boolean(snapshot?.control.paused);
  const lastError = snapshot?.control.last_error;

  if (!paused && !lastError && diagnostics.length === 0) return null;

  return (
    <section
      className="rounded-xl border border-amber-500/40 bg-amber-500/[0.04] p-4 shadow-xs"
      aria-labelledby="work-waiting-title"
    >
      <div className="flex flex-col gap-2 border-b border-amber-500/20 pb-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex gap-2.5">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <AlertTriangle className="size-4" />
          </div>
          <div>
            <h2 id="work-waiting-title" className="text-sm font-semibold text-foreground">
              {t(($) => $.cockpit.waiting_title)}
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.cockpit.waiting_description)}
            </p>
          </div>
        </div>
        <Badge variant="secondary" className="self-start font-mono text-[10px]">
          {diagnostics.length + Number(paused)} {t(($) => $.cockpit.waiting_attention_count)}
        </Badge>
      </div>

      <div className="mt-3 space-y-2">
        {paused ? (
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

        {lastError ? (
          <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-3">
            <div className="text-xs font-medium text-destructive">{t(($) => $.cockpit.waiting_control_error)}</div>
            <p className="mt-1 break-words text-xs text-muted-foreground">{lastError}</p>
          </div>
        ) : null}

        {diagnostics.map((diagnostic) => (
          <WaitingDiagnostic
            key={[diagnostic.code, diagnostic.node_key ?? "", diagnostic.updated_at].join(":")}
            diagnostic={diagnostic}
            canControl={canControl}
            {...actions}
          />
        ))}
      </div>
    </section>
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
              ? t(($) => $.cockpit.waiting_repair_continue)
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
