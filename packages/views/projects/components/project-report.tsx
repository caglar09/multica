"use client";

import { Fragment, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  BarChart3,
  Bot,
  CheckCircle2,
  ChevronRight,
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";

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

type ExecutionGroupBy = "task" | "agent" | "runtime" | "status" | "stage" | "plane" | "category" | "none";

type ExecutionGroup = {
  key: string;
  label: string;
  tasks: ProjectReportTask[];
};

const EXECUTION_GROUP_OPTIONS: Array<{ value: ExecutionGroupBy; label: string }> = [
  { value: "task", label: "Task / issue" },
  { value: "agent", label: "Agent" },
  { value: "runtime", label: "CLI / runtime" },
  { value: "status", label: "Status" },
  { value: "stage", label: "Stage" },
  { value: "plane", label: "Execution / control plane" },
  { value: "category", label: "Usage category" },
  { value: "none", label: "Ungrouped" },
];

function compactList(values: string[]): string {
  const unique = Array.from(new Set(values.filter(Boolean)));
  if (unique.length === 0) return "—";
  if (unique.length <= 2) return unique.join(", ");
  return `${unique.slice(0, 2).join(", ")} +${unique.length - 2}`;
}

function executionGroupIdentity(task: ProjectReportTask, groupBy: ExecutionGroupBy) {
  switch (groupBy) {
    case "task":
      return task.issue_id
        ? { key: `issue:${task.issue_id}`, label: task.issue_title ?? task.issue_id }
        : { key: `task:${task.id}`, label: task.issue_title ?? "Project control-plane task" };
    case "agent":
      return { key: `agent:${task.agent_id}`, label: task.agent_name };
    case "runtime": {
      const runtimeLabel = [task.runtime_provider, task.runtime_name, task.runtime_mode]
        .filter(Boolean)
        .join(" · ") || "No runtime";
      return {
        key: `runtime:${task.runtime_id ?? runtimeLabel}`,
        label: runtimeLabel,
      };
    }
    case "status":
      return { key: `status:${task.status}`, label: task.status };
    case "stage":
      return { key: `stage:${task.stage}`, label: stageLabel(task.stage) };
    case "plane":
      return { key: `plane:${task.plane}`, label: task.plane === "control" ? "Control plane" : "Execution plane" };
    case "category":
      return { key: `category:${task.category}`, label: task.category.replaceAll("_", " ") };
    default:
      return { key: `task:${task.id}`, label: task.issue_title ?? task.id };
  }
}

function groupExecutionTasks(tasks: ProjectReportTask[], groupBy: ExecutionGroupBy): ExecutionGroup[] {
  if (groupBy === "none") return [];
  const groups = new Map<string, ExecutionGroup>();
  for (const task of tasks) {
    const identity = executionGroupIdentity(task, groupBy);
    const existing = groups.get(identity.key);
    if (existing) {
      existing.tasks.push(task);
    } else {
      groups.set(identity.key, { ...identity, tasks: [task] });
    }
  }
  return Array.from(groups.values()).sort((a, b) => {
    const aTime = Math.max(...a.tasks.map((task) => new Date(task.completed_at ?? task.started_at ?? task.created_at).getTime()));
    const bTime = Math.max(...b.tasks.map((task) => new Date(task.completed_at ?? task.started_at ?? task.created_at).getTime()));
    return bTime - aTime;
  });
}

function groupRejectCount(tasks: ProjectReportTask[]): number {
  const perIssue = new Map<string, number>();
  for (const task of tasks) {
    const key = task.issue_id ?? task.id;
    perIssue.set(key, Math.max(perIssue.get(key) ?? 0, task.review_rejects));
  }
  return Array.from(perIssue.values()).reduce((sum, value) => sum + value, 0);
}

function averageNullable(values: Array<number | null>): number | null {
  const present = values.filter((value): value is number => value != null);
  if (present.length === 0) return null;
  return present.reduce((sum, value) => sum + value, 0) / present.length;
}

function latestExecutionTime(tasks: ProjectReportTask[]): string | null {
  let latest: string | null = null;
  let latestMs = -Infinity;
  for (const task of tasks) {
    const value = task.completed_at ?? task.started_at ?? task.created_at;
    const ms = new Date(value).getTime();
    if (Number.isFinite(ms) && ms > latestMs) {
      latestMs = ms;
      latest = value;
    }
  }
  return latest;
}

function ExecutionTaskRow({ task, nested = false }: { task: ProjectReportTask; nested?: boolean }) {
  return (
    <tr className={`align-top hover:bg-muted/20 ${nested ? "bg-muted/10" : ""}`}>
      <td className={`max-w-72 py-3 pr-4 ${nested ? "pl-10" : "pl-4"}`}>
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
  );
}

function ExecutionGroupRow({
  group,
  expanded,
  onToggle,
}: {
  group: ExecutionGroup;
  expanded: boolean;
  onToggle: () => void;
}) {
  const tasks = group.tasks;
  const completed = tasks.filter((task) => task.status === "completed").length;
  const failed = tasks.filter((task) => task.status === "failed").length;
  const cancelled = tasks.filter((task) => task.status === "cancelled").length;
  const active = tasks.length - completed - failed - cancelled;
  const totalTokens = tasks.reduce((sum, task) => sum + task.total_tokens, 0);
  const totalRuntime = tasks.reduce((sum, task) => sum + (task.runtime_seconds ?? 0), 0);
  const averageQueue = averageNullable(tasks.map((task) => task.queue_seconds));
  const totalCost = tasks.reduce((sum, task) => sum + task.cost_usd_ticks, 0);
  const costComplete = tasks.every((task) => task.total_tokens === 0 || task.cost_complete);
  const rejects = groupRejectCount(tasks);

  return (
    <tr className="border-t bg-muted/20 align-top hover:bg-muted/30">
      <td className="max-w-72 px-4 py-3">
        <button type="button" onClick={onToggle} className="flex w-full items-start gap-2 text-left">
          <ChevronRight className={`mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform ${expanded ? "rotate-90" : ""}`} />
          <span className="min-w-0">
            <span className="block truncate font-medium" title={group.label}>{group.label}</span>
            <span className="mt-1 block text-[11px] text-muted-foreground">
              {tasks.length} execution{tasks.length === 1 ? "" : "s"}{rejects > 0 ? ` · ${rejects} review reject${rejects === 1 ? "" : "s"}` : ""}
            </span>
          </span>
        </button>
      </td>
      <td className="px-4 py-3">
        <div className="flex flex-wrap gap-1">
          {failed > 0 && <Badge variant="destructive">{failed} failed</Badge>}
          {completed > 0 && <Badge variant="default">{completed} completed</Badge>}
          {active > 0 && <Badge variant="secondary">{active} active</Badge>}
          {cancelled > 0 && <Badge variant="outline">{cancelled} cancelled</Badge>}
        </div>
      </td>
      <td className="max-w-48 px-4 py-3 text-xs">{compactList(tasks.map((task) => task.agent_name))}</td>
      <td className="max-w-56 px-4 py-3 text-xs">
        {compactList(tasks.map((task) => [task.runtime_provider, task.runtime_name].filter(Boolean).join(" · ") || "No runtime"))}
      </td>
      <td className="max-w-48 px-4 py-3 text-xs">{compactList(tasks.flatMap((task) => task.models ? task.models.split(", ") : []))}</td>
      <td className="px-4 py-3 tabular-nums">{formatDuration(averageQueue)}</td>
      <td className="px-4 py-3 tabular-nums">{formatDuration(totalRuntime)}</td>
      <td className="px-4 py-3 tabular-nums font-medium">{formatTokens(totalTokens)}</td>
      <td className="px-4 py-3 tabular-nums">
        {totalCost > 0 ? formatCostTicks(totalCost) : "—"}
        {!costComplete && totalTokens > 0 && <p className="mt-1 text-[11px] text-muted-foreground">partial / unpriced</p>}
      </td>
      <td className="whitespace-nowrap px-4 py-3 text-xs text-muted-foreground">{formatTime(latestExecutionTime(tasks))}</td>
    </tr>
  );
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
        <Badge variant="outline">{task.category.replaceAll("_", " ")}</Badge>
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

  const [executionGroupBy, setExecutionGroupBy] = useState<ExecutionGroupBy>("task");
  const [expandedExecutionGroups, setExpandedExecutionGroups] = useState<Set<string>>(
    () => new Set(),
  );

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
  const groupedExecutions = useMemo(
    () => groupExecutionTasks(data?.tasks ?? [], executionGroupBy),
    [data?.tasks, executionGroupBy],
  );

  const toggleExecutionGroup = (key: string) => {
    setExpandedExecutionGroups((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const changeExecutionGroup = (next: ExecutionGroupBy) => {
    setExecutionGroupBy(next);
    setExpandedExecutionGroups(new Set());
  };

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
            label="Execution plane"
            value={formatTokens(summary.execution_plane_tokens)}
            detail={`${formatDuration(summary.execution_plane_runtime_seconds)} runtime · ${summary.execution_plane_cost_usd_ticks ? formatCostTicks(summary.execution_plane_cost_usd_ticks) : "—"} cost`}
            icon={<Terminal className="size-4" />}
          />
          <MetricCard
            label="Control plane"
            value={formatTokens(summary.control_plane_tokens)}
            detail={`${formatDuration(summary.control_plane_runtime_seconds)} runtime · ${summary.control_plane_cost_usd_ticks ? formatCostTicks(summary.control_plane_cost_usd_ticks) : "—"} cost`}
            icon={<Bot className="size-4" />}
          />
          <MetricCard
            label="Brain usage"
            value={formatTokens(summary.brain_learning_tokens)}
            detail={`${formatTokens(summary.brain_context_tokens)} context tokens injected${summary.brain_context_estimated ? " (estimated segment)" : ""}`}
            icon={<Gauge className="size-4" />}
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
            <CardTitle className="text-sm">Usage attribution</CardTitle>
            <CardDescription>
              Authoritative provider usage grouped by execution/control plane and durable accounting category.
            </CardDescription>
          </CardHeader>
          <CardContent className="overflow-x-auto p-0">
            <table className="w-full min-w-[900px] text-sm">
              <thead className="border-y bg-muted/30 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">Plane</th>
                  <th className="px-4 py-2 font-medium">Category</th>
                  <th className="px-4 py-2 font-medium">Tasks</th>
                  <th className="px-4 py-2 font-medium">Input</th>
                  <th className="px-4 py-2 font-medium">Output</th>
                  <th className="px-4 py-2 font-medium">Cache R/W</th>
                  <th className="px-4 py-2 font-medium">Runtime</th>
                  <th className="px-4 py-2 font-medium">Cost</th>
                  <th className="px-4 py-2 font-medium">Brain context</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {data.usage.map((bucket) => (
                  <tr key={`${bucket.plane}:${bucket.category}`} className="hover:bg-muted/20">
                    <td className="px-4 py-3"><Badge variant="outline">{bucket.plane}</Badge></td>
                    <td className="px-4 py-3 font-medium">{bucket.category.replaceAll("_", " ")}</td>
                    <td className="px-4 py-3">{bucket.task_count}</td>
                    <td className="px-4 py-3">{formatTokens(bucket.input_tokens)}</td>
                    <td className="px-4 py-3">{formatTokens(bucket.output_tokens)}</td>
                    <td className="px-4 py-3">{formatTokens(bucket.cache_read_tokens)} / {formatTokens(bucket.cache_write_tokens)}</td>
                    <td className="px-4 py-3">{formatDuration(bucket.runtime_seconds)}</td>
                    <td className="px-4 py-3">{bucket.cost_usd_ticks ? formatCostTicks(bucket.cost_usd_ticks) : "—"}</td>
                    <td className="px-4 py-3">
                      {bucket.brain_context_tokens ? formatTokens(bucket.brain_context_tokens) : "—"}
                      {bucket.brain_context_tokens > 0 && bucket.brain_context_estimated && (
                        <span className="ml-1 text-[11px] text-muted-foreground">est.</span>
                      )}
                    </td>
                  </tr>
                ))}
                {data.usage.length === 0 && (
                  <tr>
                    <td colSpan={9} className="px-4 py-8 text-center text-muted-foreground">
                      No attributed project usage yet.
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
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <CardTitle className="flex items-center gap-2 text-sm">
                  <Terminal className="size-4 text-muted-foreground" /> Execution log
                </CardTitle>
                <CardDescription className="mt-1">
                  Durable execution telemetry. Group rows aggregate executions without hiding the underlying attempts.
                </CardDescription>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-xs text-muted-foreground">Group by</span>
                <Select
                  items={EXECUTION_GROUP_OPTIONS}
                  value={executionGroupBy}
                  onValueChange={(next) => {
                    if (next) changeExecutionGroup(next as ExecutionGroupBy);
                  }}
                >
                  <SelectTrigger size="sm" className="min-w-36">
                    <SelectValue>
                      {EXECUTION_GROUP_OPTIONS.find((item) => item.value === executionGroupBy)?.label}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent align="end">
                    {EXECUTION_GROUP_OPTIONS.map((item) => (
                      <SelectItem key={item.value} value={item.value}>
                        {item.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
              <span>{data.tasks.length} execution records</span>
              {executionGroupBy !== "none" && <span>{groupedExecutions.length} groups</span>}
              <span>{summary.usage_accounted_tasks} tasks have durable project usage attribution.</span>
              <span>Review rejects come from structured <code>review.changes_requested</code> verdict events.</span>
            </div>
          </CardHeader>
          <CardContent className="overflow-x-auto p-0">
            <table className="w-full min-w-[1320px] text-sm">
              <thead className="border-y bg-muted/30 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">{executionGroupBy === "none" ? "Issue / task" : "Group"}</th>
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
                {executionGroupBy === "none" ? (
                  data.tasks.map((task) => <ExecutionTaskRow key={task.id} task={task} />)
                ) : (
                  groupedExecutions.map((group) => {
                    const expanded = expandedExecutionGroups.has(group.key);
                    return (
                      <Fragment key={group.key}>
                        <ExecutionGroupRow
                          group={group}
                          expanded={expanded}
                          onToggle={() => toggleExecutionGroup(group.key)}
                        />
                        {expanded && group.tasks.map((task) => (
                          <ExecutionTaskRow key={task.id} task={task} nested />
                        ))}
                      </Fragment>
                    );
                  })
                )}
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
