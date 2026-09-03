"use client";

import { useMemo, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  BarChart3,
  Bot,
  CheckCircle2,
  Clock3,
  Coins,
  Gauge,
  RotateCcw,
  ShieldCheck,
  Terminal,
  TimerReset,
  XCircle,
} from "lucide-react";

import type { ProjectReportTask } from "@multica/core/types";
import { projectReportOptions } from "@multica/core/projects";
import { useWorkspaceId } from "@multica/core/hooks";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";

function formatDuration(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  const seconds = Math.max(0, Math.round(value));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const restSeconds = seconds % 60;
  if (minutes < 60) return `${minutes}m ${restSeconds}s`;
  const hours = Math.floor(minutes / 60);
  const restMinutes = minutes % 60;
  if (hours < 24) return `${hours}h ${restMinutes}m`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

function formatTokens(value: number): string {
  return new Intl.NumberFormat(undefined, {
    notation: value >= 10_000 ? "compact" : "standard",
    maximumFractionDigits: 1,
  }).format(value);
}

function formatCostTicks(value: number): string {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: value > 0 && value < 10_000_000 ? 4 : 2,
    maximumFractionDigits: 4,
  }).format(value / 10_000_000_000);
}

function formatPercent(value: number): string {
  return new Intl.NumberFormat(undefined, {
    style: "percent",
    maximumFractionDigits: 1,
  }).format(Number.isFinite(value) ? value : 0);
}

function formatTime(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function statusVariant(
  status: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "failed":
    case "blocked":
      return "destructive";
    case "completed":
    case "done":
      return "default";
    case "running":
    case "dispatched":
      return "secondary";
    default:
      return "outline";
  }
}

function stageLabel(stage: string): string {
  switch (stage) {
    case "implementation":
      return "Implementation";
    case "review":
      return "Review";
    case "control_plane":
      return "Control plane";
    default:
      return "Project task";
  }
}

function MetricCard({
  label,
  value,
  detail,
  icon,
}: {
  label: string;
  value: string;
  detail: string;
  icon: ReactNode;
}) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-xs font-medium text-muted-foreground">{label}</p>
            <p className="mt-1 text-xl font-semibold tracking-tight">{value}</p>
            <p className="mt-1 text-xs text-muted-foreground">{detail}</p>
          </div>
          <div className="rounded-md border bg-muted/30 p-2 text-muted-foreground">
            {icon}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function TaskStatusDetail({ task }: { task: ProjectReportTask }) {
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant={statusVariant(task.status)}>{task.status}</Badge>
        <Badge variant="outline">{stageLabel(task.stage)}</Badge>
        {task.review_rejects > 0 && (
          <Badge variant="destructive">{task.review_rejects} reject</Badge>
        )}
      </div>
      {task.failure_reason && (
        <p className="max-w-72 truncate text-xs text-destructive" title={task.failure_reason}>
          {task.failure_reason}
        </p>
      )}
    </div>
  );
}

export function ProjectReport({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const { data, isLoading, isError } = useQuery(projectReportOptions(wsId, projectId));

  const costCoverage = data?.summary.usage_rows
    ? data.summary.costed_usage_rows / data.summary.usage_rows
    : 0;
  const failureRate = data?.summary.task_count
    ? data.summary.failed_tasks / data.summary.task_count
    : 0;
  const maxDailyTokens = useMemo(
    () => Math.max(1, ...(data?.daily.map((item) => item.total_tokens) ?? [])),
    [data?.daily],
  );

  if (isLoading) {
    return (
      <div className="h-full overflow-y-auto p-5">
        <div className="space-y-4">
          <Skeleton className="h-8 w-56" />
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {Array.from({ length: 8 }).map((_, index) => (
              <Skeleton key={index} className="h-28" />
            ))}
          </div>
          <Skeleton className="h-80" />
        </div>
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-sm text-muted-foreground">
        Project report could not be loaded.
      </div>
    );
  }

  const summary = data.summary;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto w-full max-w-[1600px] space-y-5 p-5 pb-10">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <BarChart3 className="size-5 text-muted-foreground" />
              <h2 className="text-lg font-semibold tracking-tight">Project report</h2>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              Live delivery telemetry from durable task, workflow, runtime, usage and quality-gate records.
            </p>
          </div>
          <p className="text-xs text-muted-foreground">
            Snapshot {formatTime(data.generated_at)} · refreshes every 10s
          </p>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <MetricCard
            label="Tasks"
            value={summary.task_count.toLocaleString()}
            detail={`${summary.completed_tasks} completed · ${summary.active_tasks} active`}
            icon={<Activity className="size-4" />}
          />
          <MetricCard
            label="Elapsed"
            value={formatDuration(summary.elapsed_seconds)}
            detail={`${formatDuration(summary.total_runtime_seconds)} cumulative agent time`}
            icon={<Clock3 className="size-4" />}
          />
          <MetricCard
            label="Tokens burned"
            value={formatTokens(summary.total_tokens)}
            detail={`${formatTokens(summary.input_tokens)} in · ${formatTokens(summary.output_tokens)} out`}
            icon={<Gauge className="size-4" />}
          />
          <MetricCard
            label="Provider cost"
            value={
              summary.costed_usage_rows > 0
                ? formatCostTicks(summary.authoritative_cost_usd_ticks)
                : "—"
            }
            detail={`${formatPercent(costCoverage)} of usage rows have authoritative cost`}
            icon={<Coins className="size-4" />}
          />
          <MetricCard
            label="Review rejects"
            value={summary.review_rejects.toLocaleString()}
            detail={`${summary.review_cycles} review cycles across ${summary.reviewed_issues} issues`}
            icon={<RotateCcw className="size-4" />}
          />
          <MetricCard
            label="First-pass approval"
            value={formatPercent(summary.first_pass_approval_rate)}
            detail={`${summary.first_pass_approved_issues}/${summary.approved_issues} approved without rework`}
            icon={<CheckCircle2 className="size-4" />}
          />
          <MetricCard
            label="Failure rate"
            value={formatPercent(failureRate)}
            detail={`${summary.failed_tasks} failed · ${summary.cancelled_tasks} cancelled`}
            icon={<XCircle className="size-4" />}
          />
          <MetricCard
            label="Quality gates"
            value={summary.quality_gate_total ? formatPercent(summary.quality_gate_pass_rate) : "—"}
            detail={`${summary.quality_gate_passed}/${summary.quality_gate_total} required gates passed · ${summary.blocked_workflows} blocked workflows`}
            icon={<ShieldCheck className="size-4" />}
          />
        </div>

        <div className="grid gap-4 xl:grid-cols-3">
          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">Execution latency</CardTitle>
              <CardDescription>Queue and run-time distribution for completed work.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="flex items-center justify-between gap-4 rounded-md border p-3">
                <div className="flex items-center gap-2 text-sm">
                  <TimerReset className="size-4 text-muted-foreground" /> Average queue
                </div>
                <span className="font-medium">{formatDuration(summary.average_queue_seconds)}</span>
              </div>
              <div className="flex items-center justify-between gap-4 rounded-md border p-3">
                <div className="flex items-center gap-2 text-sm">
                  <Clock3 className="size-4 text-muted-foreground" /> Average run
                </div>
                <span className="font-medium">{formatDuration(summary.average_runtime_seconds)}</span>
              </div>
              <div className="flex items-center justify-between gap-4 rounded-md border p-3">
                <div className="flex items-center gap-2 text-sm">
                  <Gauge className="size-4 text-muted-foreground" /> P95 run
                </div>
                <span className="font-medium">{formatDuration(summary.p95_runtime_seconds)}</span>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">CLI / runtime mix</CardTitle>
              <CardDescription>Which execution runtime actually performed project work.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {data.runtimes.length === 0 ? (
                <p className="text-sm text-muted-foreground">No runtime-backed tasks yet.</p>
              ) : (
                data.runtimes.slice(0, 8).map((runtime) => (
                  <div key={`${runtime.provider}:${runtime.runtime_name}:${runtime.runtime_mode}`} className="rounded-md border p-3">
                    <div className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <Terminal className="size-4 shrink-0 text-muted-foreground" />
                          <p className="truncate text-sm font-medium">{runtime.provider}</p>
                        </div>
                        <p className="mt-1 truncate text-xs text-muted-foreground">
                          {runtime.runtime_name} · {runtime.runtime_mode}
                        </p>
                      </div>
                      <div className="text-right text-xs">
                        <p className="font-medium">{runtime.task_count} tasks</p>
                        <p className="text-muted-foreground">{formatTokens(runtime.total_tokens)} tokens</p>
                      </div>
                    </div>
                  </div>
                ))
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">Recent throughput</CardTitle>
              <CardDescription>Daily task volume with token intensity, newest first.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {data.daily.slice(0, 14).map((day) => (
                <div key={day.date} className="space-y-1.5">
                  <div className="flex items-center justify-between gap-3 text-xs">
                    <span>{day.date}</span>
                    <span className="text-muted-foreground">
                      {day.completed_tasks}/{day.task_count} done · {formatTokens(day.total_tokens)} tokens
                    </span>
                  </div>
                  <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                    <div
                      className="h-full rounded-full bg-foreground/70"
                      style={{ width: `${Math.max(3, (day.total_tokens / maxDailyTokens) * 100)}%` }}
                    />
                  </div>
                </div>
              ))}
              {data.daily.length === 0 && (
                <p className="text-sm text-muted-foreground">No completed project activity yet.</p>
              )}
            </CardContent>
          </Card>
        </div>

        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-sm">
              <Bot className="size-4 text-muted-foreground" /> Agent performance
            </CardTitle>
            <CardDescription>
              Work volume, failures, run time and token consumption by agent.
            </CardDescription>
          </CardHeader>
          <CardContent className="overflow-x-auto p-0">
            <table className="w-full min-w-[760px] text-sm">
              <thead className="border-y bg-muted/30 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">Agent</th>
                  <th className="px-4 py-2 font-medium">Tasks</th>
                  <th className="px-4 py-2 font-medium">Failed</th>
                  <th className="px-4 py-2 font-medium">Avg run</th>
                  <th className="px-4 py-2 font-medium">Total run</th>
                  <th className="px-4 py-2 font-medium">Tokens</th>
                  <th className="px-4 py-2 font-medium">Provider cost</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {data.agents.map((agent) => (
                  <tr key={agent.agent_id} className="hover:bg-muted/20">
                    <td className="px-4 py-3 font-medium">{agent.agent_name}</td>
                    <td className="px-4 py-3">{agent.task_count}</td>
                    <td className="px-4 py-3">{agent.failed_tasks}</td>
                    <td className="px-4 py-3">{formatDuration(agent.average_runtime_seconds)}</td>
                    <td className="px-4 py-3">{formatDuration(agent.total_runtime_seconds)}</td>
                    <td className="px-4 py-3">{formatTokens(agent.total_tokens)}</td>
                    <td className="px-4 py-3">{agent.cost_usd_ticks ? formatCostTicks(agent.cost_usd_ticks) : "—"}</td>
                  </tr>
                ))}
                {data.agents.length === 0 && (
                  <tr>
                    <td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">
                      No agent execution records yet.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-sm">
              <Terminal className="size-4 text-muted-foreground" /> Execution log
            </CardTitle>
            <CardDescription>
              Per-task durable execution telemetry. Review rejects are counted from workflow `review.changes_requested` events.
            </CardDescription>
          </CardHeader>
          <CardContent className="overflow-x-auto p-0">
            <table className="w-full min-w-[1320px] text-sm">
              <thead className="border-y bg-muted/30 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">Issue / task</th>
                  <th className="px-4 py-2 font-medium">Status</th>
                  <th className="px-4 py-2 font-medium">Agent</th>
                  <th className="px-4 py-2 font-medium">CLI / runtime</th>
                  <th className="px-4 py-2 font-medium">Model</th>
                  <th className="px-4 py-2 font-medium">Queue</th>
                  <th className="px-4 py-2 font-medium">Run</th>
                  <th className="px-4 py-2 font-medium">Tokens</th>
                  <th className="px-4 py-2 font-medium">Cost</th>
                  <th className="px-4 py-2 font-medium">Completed</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {data.tasks.map((task) => (
                  <tr key={task.id} className="align-top hover:bg-muted/20">
                    <td className="max-w-72 px-4 py-3">
                      <p className="truncate font-medium" title={task.issue_title ?? task.id}>
                        {task.issue_title ?? "Project control-plane task"}
                      </p>
                      <p className="mt-1 font-mono text-[11px] text-muted-foreground">{task.id.slice(0, 12)}</p>
                    </td>
                    <td className="px-4 py-3"><TaskStatusDetail task={task} /></td>
                    <td className="px-4 py-3">{task.agent_name}</td>
                    <td className="max-w-56 px-4 py-3">
                      <p className="font-medium">{task.runtime_provider ?? "—"}</p>
                      <p className="mt-1 truncate text-xs text-muted-foreground" title={task.runtime_name ?? undefined}>
                        {task.runtime_name ?? "No runtime"}{task.runtime_mode ? ` · ${task.runtime_mode}` : ""}
                      </p>
                    </td>
                    <td className="max-w-48 px-4 py-3">
                      <p className="truncate text-xs" title={task.models || undefined}>{task.models || "—"}</p>
                    </td>
                    <td className="px-4 py-3 tabular-nums">{formatDuration(task.queue_seconds)}</td>
                    <td className="px-4 py-3 tabular-nums">{formatDuration(task.runtime_seconds)}</td>
                    <td className="px-4 py-3 tabular-nums">
                      <p className="font-medium">{formatTokens(task.total_tokens)}</p>
                      <p className="mt-1 text-[11px] text-muted-foreground">
                        {formatTokens(task.input_tokens)} in · {formatTokens(task.output_tokens)} out
                      </p>
                    </td>
                    <td className="px-4 py-3 tabular-nums">
                      {task.cost_usd_ticks > 0 ? formatCostTicks(task.cost_usd_ticks) : "—"}
                      {!task.cost_complete && task.total_tokens > 0 && (
                        <p className="mt-1 text-[11px] text-muted-foreground">partial / unpriced</p>
                      )}
                    </td>
                    <td className="whitespace-nowrap px-4 py-3 text-xs text-muted-foreground">
                      {formatTime(task.completed_at)}
                    </td>
                  </tr>
                ))}
                {data.tasks.length === 0 && (
                  <tr>
                    <td colSpan={10} className="px-4 py-10 text-center text-muted-foreground">
                      No task execution records for this project yet.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </CardContent>
          {data.task_truncated && (
            <div className="border-t px-4 py-3 text-xs text-muted-foreground">
              Showing the newest {data.task_limit} task records out of {summary.task_count}. Summary metrics include the full project.
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
