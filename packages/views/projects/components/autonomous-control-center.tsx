"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  Brain,
  CircleDot,
  Pause,
  Play,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  GitBranch,
  Gauge,
  CheckCircle2,
  Users,
  Workflow,
} from "lucide-react";
import { toast } from "sonner";

import type {
  AutonomousActivityItem,
  AutonomousDecision,
  AutonomousProjectSnapshot,
  AutonomousRoleRuntimeAssignment,
  AutonomousTeamMember,
  AutonomousWorkflowRun,
} from "@multica/core/types";
import {
  autonomousProjectOptions,
  useConfirmAutonomousTeam,
  usePauseAutonomousProject,
  useReplanAutonomousProject,
  useResumeAutonomousProject,
  useRetryAutonomousAction,
  useResolveAutonomousEscalation,
} from "@multica/core/projects";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { useNavigation } from "../../navigation";

function formatTime(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function formatRelative(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  const diff = Date.now() - date.getTime();
  if (!Number.isFinite(diff)) return value;
  const minutes = Math.round(diff / 60000);
  if (Math.abs(minutes) < 1) return "now";
  if (minutes < 60) return String(minutes) + "m ago";
  const hours = Math.round(minutes / 60);
  if (hours < 24) return String(hours) + "h ago";
  return String(Math.round(hours / 24)) + "d ago";
}

function healthVariant(
  status: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "attention":
      return "destructive";
    case "running":
      return "default";
    case "paused":
      return "secondary";
    default:
      return "outline";
  }
}

function statusDotClass(status: string): string {
  switch (status) {
    case "running":
    case "dispatched":
      return "bg-emerald-500";
    case "queued":
    case "waiting_local_directory":
    case "deferred":
      return "bg-amber-500";
    case "failed":
    case "blocked":
      return "bg-destructive";
    case "completed":
    case "done":
      return "bg-sky-500";
    default:
      return "bg-muted-foreground/60";
  }
}

function isReplanPending(snapshot: AutonomousProjectSnapshot): boolean {
  const requested = snapshot.control.replan_requested_at;
  if (!requested) return false;
  const completed = snapshot.control.replan_completed_at;
  if (!completed) return true;
  return new Date(completed).getTime() < new Date(requested).getTime();
}

function TeamDraftConfigurator({
  snapshot,
  canControl,
  isPending,
  onConfirm,
  onOpenSkills,
}: {
  snapshot: AutonomousProjectSnapshot;
  canControl: boolean;
  isPending: boolean;
  onConfirm: (assignments: AutonomousRoleRuntimeAssignment[]) => void;
  onOpenSkills: () => void;
}) {
  const draft = snapshot.draft;
  const roles = draft?.plan.roles ?? [];
  const [assignments, setAssignments] = useState<
    Record<string, { runtime_id: string; skill_ids: string[] }>
  >({});

  useEffect(() => {
    if (!draft) {
      setAssignments({});
      return;
    }
    const fallbackRuntime =
      draft.default_runtime_id ??
      snapshot.runtimes.find((runtime) => runtime.status === "online")?.id ??
      "";
    const next: Record<string, { runtime_id: string; skill_ids: string[] }> = {};
    for (const role of draft.plan.roles ?? []) {
      next[role.role] = {
        runtime_id: fallbackRuntime,
        skill_ids: [...draft.default_skill_ids],
      };
    }
    setAssignments(next);
  }, [draft?.updated_at, draft?.default_runtime_id, snapshot.runtimes.length]);

  if (!draft) return null;

  const provisioning = draft.status === "provisioning";
  const allConfigured =
    roles.length > 0 &&
    roles.every((role) => Boolean(assignments[role.role]?.runtime_id));

  const setRuntime = (role: string, runtimeId: string) => {
    setAssignments((current) => ({
      ...current,
      [role]: {
        runtime_id: runtimeId,
        skill_ids: current[role]?.skill_ids ?? [],
      },
    }));
  };

  const toggleSkill = (role: string, skillId: string) => {
    setAssignments((current) => {
      const entry = current[role] ?? { runtime_id: "", skill_ids: [] };
      const selected = entry.skill_ids.includes(skillId);
      return {
        ...current,
        [role]: {
          ...entry,
          skill_ids: selected
            ? entry.skill_ids.filter((id) => id !== skillId)
            : [...entry.skill_ids, skillId],
        },
      };
    });
  };

  const submit = () => {
    if (!allConfigured || provisioning || isPending) return;
    onConfirm(
      roles.map((role) => ({
        role: role.role,
        runtime_id: assignments[role.role]?.runtime_id ?? "",
        skill_ids: assignments[role.role]?.skill_ids ?? [],
      })),
    );
  };

  return (
    <Card className="mb-5 border-primary/30">
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Users className="size-4" />
              Configure the proposed Technology Team
              <Badge variant={provisioning ? "secondary" : "outline"}>
                {provisioning ? "creating team" : "approval required"}
              </Badge>
            </CardTitle>
            <CardDescription className="mt-1 max-w-3xl">
              The runtime-backed planner has proposed the roles, but no
              specialist agent is created until you choose which CLI/runtime
              each role will use. Workspace skills selected here are attached
              during creation; runtime-local skills are inherited automatically.
            </CardDescription>
          </div>
          <div className="text-right text-caption text-muted-foreground">
            <div>{draft.planner_name}</div>
            <div>{draft.planner_model ?? "runtime default"}</div>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-3">
          {roles.map((role) => {
            const selected = assignments[role.role] ?? {
              runtime_id: "",
              skill_ids: [],
            };
            return (
              <div
                key={role.role}
                className="rounded-xl border bg-muted/10 p-3"
              >
                <div className="flex flex-col gap-3 lg:grid lg:grid-cols-[minmax(220px,1fr)_minmax(220px,0.8fr)_minmax(260px,1.2fr)]">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{role.display_name}</span>
                      <Badge variant="outline">{role.family}</Badge>
                    </div>
                    <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                      {role.role}
                    </div>
                    {role.reason ? (
                      <p className="mt-2 text-caption text-muted-foreground">
                        {role.reason}
                      </p>
                    ) : null}
                  </div>

                  <label className="block">
                    <span className="mb-1.5 block text-caption font-medium text-muted-foreground">
                      CLI / Runtime
                    </span>
                    <select
                      value={selected.runtime_id}
                      onChange={(event) =>
                        setRuntime(role.role, event.target.value)
                      }
                      disabled={!canControl || provisioning || isPending}
                      className="h-9 w-full rounded-md border bg-background px-2 text-body"
                    >
                      <option value="">Select runtime…</option>
                      {snapshot.runtimes.map((runtime) => (
                        <option
                          key={runtime.id}
                          value={runtime.id}
                          disabled={runtime.status !== "online"}
                        >
                          {runtime.provider} · {runtime.name}
                          {runtime.status === "online"
                            ? ""
                            : " · " + runtime.status}
                        </option>
                      ))}
                    </select>
                  </label>

                  <div>
                    <div className="mb-1.5 flex items-center justify-between gap-2">
                      <span className="text-caption font-medium text-muted-foreground">
                        Workspace Skills
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={onOpenSkills}
                      >
                        Manage skills
                      </Button>
                    </div>
                    {snapshot.skills.length > 0 ? (
                      <div className="max-h-28 space-y-1 overflow-y-auto rounded-md border bg-background p-2">
                        {snapshot.skills.map((skill) => (
                          <label
                            key={skill.id}
                            className="flex cursor-pointer items-start gap-2 rounded px-1 py-1 text-caption hover:bg-muted/40"
                            title={skill.description}
                          >
                            <input
                              type="checkbox"
                              checked={selected.skill_ids.includes(skill.id)}
                              onChange={() => toggleSkill(role.role, skill.id)}
                              disabled={!canControl || provisioning || isPending}
                              className="mt-0.5"
                            />
                            <span>
                              <span className="font-medium">{skill.name}</span>
                              {skill.description ? (
                                <span className="ml-1 text-muted-foreground">
                                  — {skill.description}
                                </span>
                              ) : null}
                            </span>
                          </label>
                        ))}
                      </div>
                    ) : (
                      <div className="rounded-md border border-dashed p-2 text-caption text-muted-foreground">
                        No workspace skills exist yet. Create reusable skills
                        before confirming if this team needs shared procedures
                        or domain knowledge.
                      </div>
                    )}
                  </div>
                </div>
              </div>
            );
          })}
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-3">
          <p className="text-caption text-muted-foreground">
            Defaults inherit Mika&apos;s runtime and Mika&apos;s enabled workspace
            skills. You can change each generated agent later from Agent Detail.
          </p>
          {canControl ? (
            <Button
              onClick={submit}
              disabled={!allConfigured || provisioning || isPending}
            >
              <Users />
              {provisioning || isPending
                ? "Creating team…"
                : "Create team & continue"}
            </Button>
          ) : (
            <Badge variant="outline">Owner/admin approval required</Badge>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
function MetricCard({
  title,
  value,
  description,
  icon,
}: {
  title: string;
  value: string | number;
  description?: string;
  icon: ReactNode;
}) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-body">
          <span className="text-muted-foreground">{icon}</span>
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="text-display-xs font-semibold tabular-nums">{value}</div>
        {description ? (
          <div className="mt-1 text-caption text-muted-foreground">{description}</div>
        ) : null}
      </CardContent>
    </Card>
  );
}

function TeamMemberCard({
  member,
  onOpenAgent,
}: {
  member: AutonomousTeamMember;
  onOpenAgent: () => void;
}) {
  const working = Boolean(member.current_task_id);
  return (
    <Card size="sm" className={cn(!member.active && "opacity-60")}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <span
            className={cn(
              "size-2 rounded-full",
              working ? "bg-emerald-500" : "bg-muted-foreground/50",
            )}
          />
          <button
            type="button"
            onClick={onOpenAgent}
            className="truncate text-left hover:underline"
          >
            {member.agent_name}
          </button>
        </CardTitle>
        <CardDescription className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline">{member.family}</Badge>
          <span className="font-mono text-[11px]">{member.role}</span>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div>
          <div className="mb-1 text-caption font-medium text-muted-foreground">
            Capabilities
          </div>
          <div className="flex flex-wrap gap-1">
            {member.capabilities.length > 0 ? (
              member.capabilities.map((capability) => (
                <Badge key={capability} variant="secondary">
                  {capability}
                </Badge>
              ))
            ) : (
              <span className="text-caption text-muted-foreground">
                No declared capabilities
              </span>
            )}
          </div>
        </div>

        {member.reason ? (
          <div>
            <div className="mb-1 text-caption font-medium text-muted-foreground">
              Why this role exists
            </div>
            <p className="text-body leading-relaxed">{member.reason}</p>
          </div>
        ) : null}

        <div className="rounded-lg border bg-muted/30 p-2.5">
          <div className="flex items-center justify-between gap-2 text-caption">
            <span className="font-medium">Current work</span>
            {member.current_task_status ? (
              <span className="inline-flex items-center gap-1 text-muted-foreground">
                <span
                  className={cn(
                    "size-1.5 rounded-full",
                    statusDotClass(member.current_task_status),
                  )}
                />
                {member.current_task_status}
              </span>
            ) : null}
          </div>
          <div className="mt-1 text-body text-muted-foreground">
            {member.current_task_title ?? "Idle"}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function WorkflowCard({
  run,
  onOpenIssue,
}: {
  run: AutonomousWorkflowRun;
  onOpenIssue: () => void;
}) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <button
            type="button"
            onClick={onOpenIssue}
            className="text-left hover:underline"
          >
            {run.issue_title}
          </button>
        </CardTitle>
        <CardDescription className="flex flex-wrap gap-1.5">
          <Badge variant={run.state === "blocked" ? "destructive" : "outline"}>
            {run.state}
          </Badge>
          <Badge variant="secondary">review {run.review_cycles}</Badge>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2 text-caption">
        <div className="grid grid-cols-[88px_1fr] gap-2">
          <span className="text-muted-foreground">Owner</span>
          <span>{run.owner_agent_name ?? "Unassigned"}</span>
          <span className="text-muted-foreground">Reviewer</span>
          <span>{run.reviewer_agent_name ?? "Unassigned"}</span>
          <span className="text-muted-foreground">Actions</span>
          <span>
            {run.pending_actions} pending · {run.failed_actions} failed
          </span>
          <span className="text-muted-foreground">Updated</span>
          <span>{formatRelative(run.updated_at)}</span>
        </div>
      </CardContent>
    </Card>
  );
}

function ActivityRow({
  item,
  onOpenIssue,
  onOpenAgent,
}: {
  item: AutonomousActivityItem;
  onOpenIssue?: () => void;
  onOpenAgent?: () => void;
}) {
  return (
    <div className="relative grid grid-cols-[18px_1fr_auto] gap-3 py-3">
      <div className="relative flex justify-center">
        <span className="mt-1.5 size-2 rounded-full bg-foreground/60" />
      </div>
      <div className="min-w-0">
        <div className="font-medium">{item.title}</div>
        {item.detail ? (
          <div className="mt-0.5 text-caption text-muted-foreground">
            {item.detail}
          </div>
        ) : null}
        <div className="mt-1 flex flex-wrap gap-2">
          {item.issue_id ? (
            <button
              type="button"
              onClick={onOpenIssue}
              className="text-caption text-primary hover:underline"
            >
              Open issue
            </button>
          ) : null}
          {item.agent_id ? (
            <button
              type="button"
              onClick={onOpenAgent}
              className="text-caption text-primary hover:underline"
            >
              Open agent
            </button>
          ) : null}
        </div>
      </div>
      <time
        className="whitespace-nowrap text-caption text-muted-foreground"
        title={formatTime(item.created_at)}
      >
        {formatRelative(item.created_at)}
      </time>
    </div>
  );
}

function DecisionCard({ decision }: { decision: AutonomousDecision }) {
  const roles = decision.plan.roles ?? [];
  return (
    <details className="group rounded-xl border bg-surface">
      <summary className="cursor-pointer list-none px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <div className="font-medium">
              {decision.plan.summary || "Team plan evaluated"}
            </div>
            <div className="mt-1 flex flex-wrap gap-1.5 text-caption text-muted-foreground">
              <span>{decision.planner_name}</span>
              {decision.planner_model ? <span>· {decision.planner_model}</span> : null}
              <span>
                · {decision.source_type} rev {decision.source_revision}
              </span>
            </div>
          </div>
          <span className="text-caption text-muted-foreground">
            {formatTime(decision.created_at)}
          </span>
        </div>
      </summary>
      <div className="border-t px-4 py-3">
        {decision.plan.intent ? (
          <p className="mb-3 text-body text-muted-foreground">
            {decision.plan.intent}
          </p>
        ) : null}
        <div className="space-y-2">
          {roles.map((role) => (
            <div
              key={role.role}
              className="rounded-lg border bg-muted/20 p-3"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{role.display_name}</span>
                <Badge variant="outline">{role.family}</Badge>
                <span className="font-mono text-[11px] text-muted-foreground">
                  {role.role}
                </span>
              </div>
              {role.reason ? (
                <p className="mt-1 text-caption text-muted-foreground">
                  {role.reason}
                </p>
              ) : null}
              {role.capabilities && role.capabilities.length > 0 ? (
                <div className="mt-2 flex flex-wrap gap-1">
                  {role.capabilities.map((capability) => (
                    <Badge key={capability} variant="secondary">
                      {capability}
                    </Badge>
                  ))}
                </div>
              ) : null}
            </div>
          ))}
        </div>
      </div>
    </details>
  );
}

export function AutonomousControlCenter({
  projectId,
  canControl = false,
}: {
  projectId: string;
  canControl?: boolean;
}) {
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const router = useNavigation();
  const { data, isLoading, isError, refetch, isFetching } = useQuery(
    autonomousProjectOptions(wsId, projectId),
  );
  const pause = usePauseAutonomousProject();
  const resume = useResumeAutonomousProject();
  const replan = useReplanAutonomousProject();
  const confirmTeam = useConfirmAutonomousTeam();
  const retryAction = useRetryAutonomousAction();
  const resolveEscalation = useResolveAutonomousEscalation();

  const replanPending = data ? isReplanPending(data) : false;

  const groupedRuns = useMemo(() => {
    const groups: Record<
      "development" | "review" | "done" | "blocked",
      AutonomousWorkflowRun[]
    > = {
      development: [],
      review: [],
      done: [],
      blocked: [],
    };
    for (const run of data?.workflows ?? []) {
      switch (run.state) {
        case "in_review":
          groups.review.push(run);
          break;
        case "done":
          groups.done.push(run);
          break;
        case "blocked":
          groups.blocked.push(run);
          break;
        default:
          groups.development.push(run);
      }
    }
    return groups;
  }, [data?.workflows]);

  if (isLoading) {
    return (
      <div className="h-full overflow-y-auto p-5">
        <div className="mx-auto max-w-6xl space-y-4">
          <Skeleton className="h-9 w-72" />
          <div className="grid gap-3 md:grid-cols-4">
            {Array.from({ length: 4 }).map((_, index) => (
              <Skeleton key={index} className="h-28" />
            ))}
          </div>
          <Skeleton className="h-80 w-full" />
        </div>
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <Card className="max-w-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <AlertTriangle />
              Control Center unavailable
            </CardTitle>
            <CardDescription>
              The autonomous project snapshot could not be loaded. Confirm the
              latest database migrations and backend are running.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => void refetch()}>Retry</Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  const controlMutationPending =
    pause.isPending || resume.isPending || replan.isPending || confirmTeam.isPending;

  const handlePauseResume = () => {
    const mutation = data.control.paused ? resume : pause;
    mutation.mutate(projectId, {
      onSuccess: () =>
        toast.success(
          data.control.paused
            ? "Autonomous project resumed"
            : "Autonomous project paused",
        ),
      onError: () =>
        toast.error("Could not update autonomous project state"),
    });
  };

  const handleReplan = () => {
    replan.mutate(projectId, {
      onSuccess: () => toast.success("Runtime team replan requested"),
      onError: () => toast.error("Could not request team replan"),
    });
  };

  const handleConfirmTeam = (
    assignments: AutonomousRoleRuntimeAssignment[],
  ) => {
    confirmTeam.mutate(
      { projectId, assignments },
      {
        onSuccess: () =>
          toast.success("Team configuration accepted; agents are being created"),
        onError: () =>
          toast.error("Could not confirm autonomous team configuration"),
      },
    );
  };

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto w-full max-w-7xl p-4 md:p-6">
        <div className="mb-5 flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-title-lg font-semibold">
                Autonomous Control Center
              </h2>
              <Badge variant={healthVariant(data.health.status)}>
                {data.health.status}
              </Badge>
              {data.control.paused ? (
                <Badge variant="secondary">dispatch paused</Badge>
              ) : null}
              {replanPending ? (
                <Badge variant="outline">replanning</Badge>
              ) : null}
            </div>
            <p className="mt-1 text-body text-muted-foreground">
              Observe LLM staffing decisions, workflow state, task activity and
              intervention controls.
            </p>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => void refetch()}
              disabled={isFetching}
            >
              <RefreshCw className={cn(isFetching && "animate-spin")} />
              Refresh
            </Button>
            {canControl ? (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleReplan}
                  disabled={
                    controlMutationPending ||
                    replanPending ||
                    !data.team
                  }
                >
                  <Brain />
                  {replanPending ? "Replanning…" : "Replan team"}
                </Button>
                <Button
                  variant={data.control.paused ? "default" : "secondary"}
                  size="sm"
                  onClick={handlePauseResume}
                  disabled={controlMutationPending}
                >
                  {data.control.paused ? <Play /> : <Pause />}
                  {data.control.paused ? "Resume" : "Pause"}
                </Button>
              </>
            ) : null}
          </div>
        </div>

        {data.draft ? (
          <TeamDraftConfigurator
            snapshot={data}
            canControl={canControl}
            isPending={confirmTeam.isPending}
            onConfirm={handleConfirmTeam}
            onOpenSkills={() => router.push(wsPaths.skills())}
          />
        ) : null}

        {data.control.last_error ? (
          <div className="mb-4 rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-body text-destructive">
            <div className="font-medium">Latest control-plane error</div>
            <div className="mt-1 break-words text-caption">
              {data.control.last_error}
            </div>
          </div>
        ) : null}

        <Tabs defaultValue="overview" className="min-h-0">
          <TabsList variant="line" className="mb-4 max-w-full overflow-x-auto">
            <TabsTrigger value="overview">
              <CircleDot /> Overview
            </TabsTrigger>
            <TabsTrigger value="team">
              <Users /> Team
            </TabsTrigger>
            <TabsTrigger value="plan">
              <GitBranch /> Project OS
            </TabsTrigger>
            <TabsTrigger value="workflow">
              <Workflow /> Workflow
            </TabsTrigger>
            <TabsTrigger value="activity">
              <Activity /> Activity
            </TabsTrigger>
            <TabsTrigger value="decisions">
              <Brain /> Decisions
            </TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <MetricCard
                title="Team"
                value={
                  data.team?.members.filter((member) => member.active).length ??
                  0
                }
                description={data.team?.intent || "No autonomous team yet"}
                icon={<Users className="size-4" />}
              />
              <MetricCard
                title="Active workflows"
                value={data.health.active_workflows}
                description={String(data.health.blocked) + " blocked"}
                icon={<Workflow className="size-4" />}
              />
              <MetricCard
                title="Failed actions"
                value={data.health.failed_actions}
                description="Durable orchestration actions"
                icon={<AlertTriangle className="size-4" />}
              />
              <MetricCard
                title="Last planned"
                value={
                  data.team?.last_planned_at
                    ? formatRelative(data.team.last_planned_at)
                    : "—"
                }
                description={
                  data.team?.planner_model ??
                  data.team?.planner_name ??
                  "No plan"
                }
                icon={<Brain className="size-4" />}
              />
            </div>

            <div className="grid gap-4 lg:grid-cols-[1.2fr_0.8fr]">
              <Card>
                <CardHeader>
                  <CardTitle>Current organization</CardTitle>
                  <CardDescription>
                    LLM-created roles are reused across issue revisions; missing
                    specialists are added add-only.
                  </CardDescription>
                </CardHeader>
                <CardContent>
                  {data.team ? (
                    <div className="grid gap-2 sm:grid-cols-2">
                      {data.team.members
                        .filter((member) => member.active)
                        .map((member) => (
                          <button
                            key={member.role}
                            type="button"
                            onClick={() =>
                              router.push(
                                wsPaths.agentDetail(member.agent_id),
                              )
                            }
                            className="flex items-center gap-3 rounded-lg border p-3 text-left hover:bg-muted/40"
                          >
                            <span
                              className={cn(
                                "size-2 rounded-full",
                                member.current_task_id
                                  ? "bg-emerald-500"
                                  : "bg-muted-foreground/50",
                              )}
                            />
                            <span className="min-w-0">
                              <span className="block truncate font-medium">
                                {member.agent_name}
                              </span>
                              <span className="block truncate text-caption text-muted-foreground">
                                {member.family} · {member.role}
                              </span>
                            </span>
                          </button>
                        ))}
                    </div>
                  ) : (
                    <div className="rounded-lg border border-dashed p-6 text-center text-muted-foreground">
                      {data.draft
                        ? "The team plan is ready and waiting for runtime configuration above."
                        : "No Technology Team has been provisioned yet. Project creation can trigger runtime-backed staffing."}
                    </div>
                  )}
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle>Control state</CardTitle>
                  <CardDescription>
                    Durable control-plane state for this project.
                  </CardDescription>
                </CardHeader>
                <CardContent className="space-y-2 text-body">
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Dispatch</span>
                    <span>{data.control.paused ? "Paused" : "Running"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Plan revision</span>
                    <span>{data.team?.plan_revision ?? "—"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Planner</span>
                    <span>{data.team?.planner_name ?? "—"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Runtime / model</span>
                    <span className="truncate">
                      {data.team?.planner_model ?? "default / fallback"}
                    </span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">
                      Replan requested
                    </span>
                    <span>{formatTime(data.control.replan_requested_at)}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">
                      Replan completed
                    </span>
                    <span>{formatTime(data.control.replan_completed_at)}</span>
                  </div>
                </CardContent>
              </Card>
            </div>
          </TabsContent>

          <TabsContent value="team">
            {data.team ? (
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                {data.team.members.map((member) => (
                  <TeamMemberCard
                    key={member.role}
                    member={member}
                    onOpenAgent={() =>
                      router.push(wsPaths.agentDetail(member.agent_id))
                    }
                  />
                ))}
              </div>
            ) : (
              <Card>
                <CardContent className="py-10 text-center text-muted-foreground">
                  No autonomous Technology Team exists for this project yet.
                </CardContent>
              </Card>
            )}
          </TabsContent>

          <TabsContent value="plan" className="space-y-4">
            {data.plan ? (
              <>
                <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                  <MetricCard
                    title="Plan revision"
                    value={data.plan.revision}
                    description={data.plan.planner_model ?? data.plan.planner_name}
                    icon={<GitBranch className="size-4" />}
                  />
                  <MetricCard
                    title="Plan nodes"
                    value={data.plan.nodes.length}
                    description={
                      String(
                        data.plan.nodes.filter((node) => node.status === "completed")
                          .length,
                      ) + " completed"
                    }
                    icon={<Workflow className="size-4" />}
                  />
                  <MetricCard
                    title="Open escalations"
                    value={data.escalations.filter(
                      (item) =>
                        item.status === "open" || item.status === "acknowledged",
                    ).length}
                    description="Policy, budget and execution gates"
                    icon={<AlertTriangle className="size-4" />}
                  />
                  <MetricCard
                    title="Attempt budget"
                    value={
                      data.budget
                        ? String(data.budget.total_attempts) +
                          "/" +
                          String(data.budget.max_total_attempts)
                        : "—"
                    }
                    description={
                      data.budget
                        ? String(data.budget.max_parallel_nodes) +
                          " max parallel nodes"
                        : "No budget policy"
                    }
                    icon={<Gauge className="size-4" />}
                  />
                </div>

                <Card>
                  <CardHeader>
                    <CardTitle>{data.plan.goal}</CardTitle>
                    <CardDescription>
                      Durable project plan revision {data.plan.revision} ·{" "}
                      {data.plan.status}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-4">
                    <div className="rounded-lg border bg-muted/20 p-3">
                      <div className="text-caption font-medium text-muted-foreground">
                        Specification
                      </div>
                      <p className="mt-1 text-body">
                        {data.plan.specification.summary ?? "No summary"}
                      </p>
                      {(data.plan.specification.definition_of_done?.length ?? 0) >
                      0 ? (
                        <div className="mt-3">
                          <div className="text-caption font-medium text-muted-foreground">
                            Definition of Done
                          </div>
                          <ul className="mt-1 list-disc space-y-1 pl-5 text-caption">
                            {data.plan.specification.definition_of_done?.map(
                              (item) => <li key={item}>{item}</li>,
                            )}
                          </ul>
                        </div>
                      ) : null}
                    </div>

                    <div className="space-y-2">
                      {data.plan.nodes.map((node) => {
                        const incoming = data.plan?.edges.filter(
                          (edge) => edge.to === node.key,
                        );
                        return (
                          <div
                            key={node.id}
                            className="rounded-lg border bg-surface p-3"
                          >
                            <div className="flex flex-wrap items-start justify-between gap-3">
                              <div className="min-w-0">
                                <div className="flex flex-wrap items-center gap-2">
                                  <span
                                    className={cn(
                                      "size-2 rounded-full",
                                      statusDotClass(node.status),
                                    )}
                                  />
                                  <span className="font-medium">{node.title}</span>
                                  <Badge variant="outline">{node.kind}</Badge>
                                  <Badge
                                    variant={
                                      node.risk === "critical" ||
                                      node.risk === "high"
                                        ? "destructive"
                                        : "secondary"
                                    }
                                  >
                                    {node.risk}
                                  </Badge>
                                </div>
                                <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                                  {node.key}
                                </div>
                              </div>
                              <div className="flex items-center gap-2">
                                <Badge variant="outline">{node.status}</Badge>
                                {node.materialized_issue_id ? (
                                  <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={() =>
                                      router.push(
                                        wsPaths.issueDetail(
                                          node.materialized_issue_id as string,
                                        ),
                                      )
                                    }
                                  >
                                    Open issue
                                  </Button>
                                ) : null}
                              </div>
                            </div>
                            <div className="mt-3 grid gap-2 text-caption sm:grid-cols-3">
                              <div>
                                <span className="text-muted-foreground">Role</span>
                                <div>
                                  {node.assigned_role ??
                                    node.required_role_family ??
                                    "scheduler"}
                                </div>
                              </div>
                              <div>
                                <span className="text-muted-foreground">
                                  Attempts
                                </span>
                                <div>
                                  {node.attempt}/{node.max_attempts}
                                </div>
                              </div>
                              <div>
                                <span className="text-muted-foreground">
                                  Dependencies
                                </span>
                                <div>
                                  {incoming && incoming.length > 0
                                    ? incoming.map((edge) => edge.from).join(", ")
                                    : "none"}
                                </div>
                              </div>
                            </div>
                            {node.acceptance_criteria.length > 0 ? (
                              <div className="mt-3">
                                <div className="text-caption font-medium text-muted-foreground">
                                  Acceptance criteria
                                </div>
                                <ul className="mt-1 list-disc space-y-1 pl-5 text-caption">
                                  {node.acceptance_criteria.map((criterion) => (
                                    <li key={criterion}>{criterion}</li>
                                  ))}
                                </ul>
                              </div>
                            ) : null}
                          </div>
                        );
                      })}
                    </div>
                  </CardContent>
                </Card>

                {data.escalations.length > 0 ? (
                  <Card>
                    <CardHeader>
                      <CardTitle className="flex items-center gap-2">
                        <AlertTriangle className="size-4" />
                        Escalations & approvals
                      </CardTitle>
                      <CardDescription>
                        Irreversible or policy-sensitive work remains blocked
                        until the backend records an explicit resolution.
                      </CardDescription>
                    </CardHeader>
                    <CardContent className="space-y-2">
                      {data.escalations.map((item) => {
                        const open =
                          item.status === "open" ||
                          item.status === "acknowledged";
                        return (
                          <div
                            key={item.id}
                            className="rounded-lg border p-3"
                          >
                            <div className="flex flex-wrap items-start justify-between gap-3">
                              <div>
                                <div className="flex flex-wrap items-center gap-2">
                                  <span className="font-medium">{item.summary}</span>
                                  <Badge
                                    variant={
                                      item.severity === "critical" ||
                                      item.severity === "high"
                                        ? "destructive"
                                        : "outline"
                                    }
                                  >
                                    {item.severity}
                                  </Badge>
                                  <Badge variant="secondary">{item.category}</Badge>
                                </div>
                                <div className="mt-1 text-caption text-muted-foreground">
                                  {formatTime(item.opened_at)} · {item.status}
                                </div>
                              </div>
                              {canControl &&
                              open &&
                              item.category === "approval_required" ? (
                                <div className="flex gap-2">
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    disabled={resolveEscalation.isPending}
                                    onClick={() =>
                                      resolveEscalation.mutate(
                                        {
                                          projectId,
                                          escalationId: item.id,
                                          decision: "rejected",
                                        },
                                        {
                                          onSuccess: () =>
                                            toast.success("Approval rejected"),
                                          onError: () =>
                                            toast.error(
                                              "Could not resolve approval",
                                            ),
                                        },
                                      )
                                    }
                                  >
                                    Reject
                                  </Button>
                                  <Button
                                    size="sm"
                                    disabled={resolveEscalation.isPending}
                                    onClick={() =>
                                      resolveEscalation.mutate(
                                        {
                                          projectId,
                                          escalationId: item.id,
                                          decision: "approved",
                                        },
                                        {
                                          onSuccess: () =>
                                            toast.success(
                                              "Approved; scheduler can continue",
                                            ),
                                          onError: () =>
                                            toast.error(
                                              "Could not resolve approval",
                                            ),
                                        },
                                      )
                                    }
                                  >
                                    <CheckCircle2 /> Approve
                                  </Button>
                                </div>
                              ) : null}
                            </div>
                          </div>
                        );
                      })}
                    </CardContent>
                  </Card>
                ) : null}

                <Card>
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                      <ShieldCheck className="size-4" />
                      Quality evidence
                    </CardTitle>
                    <CardDescription>
                      Backend-owned evidence generated by review, QA, security
                      and integration stages.
                    </CardDescription>
                  </CardHeader>
                  <CardContent>
                    {data.quality_gates.length > 0 ? (
                      <div className="grid gap-2 md:grid-cols-2">
                        {data.quality_gates.map((gate) => (
                          <div
                            key={gate.id}
                            className="flex items-center justify-between gap-3 rounded-lg border p-3"
                          >
                            <div>
                              <div className="font-medium">{gate.gate_type}</div>
                              <div className="text-caption text-muted-foreground">
                                {formatTime(gate.updated_at)}
                              </div>
                            </div>
                            <Badge
                              variant={
                                gate.status === "failed"
                                  ? "destructive"
                                  : gate.status === "passed"
                                    ? "default"
                                    : "outline"
                              }
                            >
                              {gate.status}
                            </Badge>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <div className="py-6 text-center text-muted-foreground">
                        Quality evidence will appear as lifecycle gates run.
                      </div>
                    )}
                  </CardContent>
                </Card>
              </>
            ) : (
              <Card>
                <CardContent className="py-10 text-center text-muted-foreground">
                  The durable project plan has not been generated yet.
                </CardContent>
              </Card>
            )}
          </TabsContent>

          <TabsContent value="workflow" className="space-y-4">
            <div className="grid gap-3 xl:grid-cols-4">
              {[
                ["development", "Development"],
                ["review", "Review"],
                ["blocked", "Blocked"],
                ["done", "Done"],
              ].map(([key, label]) => {
                const runs =
                  groupedRuns[
                    key as "development" | "review" | "blocked" | "done"
                  ];
                return (
                  <div key={key} className="min-w-0">
                    <div className="mb-2 flex items-center justify-between">
                      <h3 className="text-body font-medium">{label}</h3>
                      <Badge variant="outline">{runs.length}</Badge>
                    </div>
                    <div className="space-y-2">
                      {runs.map((run) => (
                        <WorkflowCard
                          key={run.id}
                          run={run}
                          onOpenIssue={() =>
                            router.push(wsPaths.issueDetail(run.issue_id))
                          }
                        />
                      ))}
                      {runs.length === 0 ? (
                        <div className="rounded-lg border border-dashed p-4 text-center text-caption text-muted-foreground">
                          Empty
                        </div>
                      ) : null}
                    </div>
                  </div>
                );
              })}
            </div>

            {data.actions.some((action) => action.status === "failed") ? (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <AlertTriangle className="size-4 text-destructive" />
                    Failed orchestration actions
                  </CardTitle>
                  <CardDescription>
                    Retry after the underlying runtime or configuration problem
                    has been corrected.
                  </CardDescription>
                </CardHeader>
                <CardContent className="space-y-2">
                  {data.actions
                    .filter((action) => action.status === "failed")
                    .map((action) => (
                      <div
                        key={action.id}
                        className="flex flex-col gap-2 rounded-lg border p-3 sm:flex-row sm:items-center sm:justify-between"
                      >
                        <div className="min-w-0">
                          <div className="font-medium">
                            {action.action_type}
                          </div>
                          <div className="text-caption text-muted-foreground">
                            attempt {action.attempts}/{action.max_attempts} ·{" "}
                            {formatTime(action.updated_at)}
                          </div>
                          {action.last_error ? (
                            <div className="mt-1 break-words text-caption text-destructive">
                              {action.last_error}
                            </div>
                          ) : null}
                        </div>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={
                            !canControl ||
                            retryAction.isPending ||
                            data.control.paused
                          }
                          onClick={() =>
                            retryAction.mutate(
                              { projectId, actionId: action.id },
                              {
                                onSuccess: () =>
                                  toast.success(
                                    "Workflow action queued for retry",
                                  ),
                                onError: () =>
                                  toast.error(
                                    "Could not retry workflow action",
                                  ),
                              },
                            )
                          }
                        >
                          <RotateCcw /> Retry
                        </Button>
                      </div>
                    ))}
                </CardContent>
              </Card>
            ) : null}
          </TabsContent>

          <TabsContent value="activity">
            <Card>
              <CardHeader>
                <CardTitle>Autonomous activity timeline</CardTitle>
                <CardDescription>
                  Team planning, task execution and workflow transitions in
                  reverse chronological order.
                </CardDescription>
              </CardHeader>
              <CardContent>
                {data.activity.length > 0 ? (
                  <div className="divide-y">
                    {data.activity.map((item) => (
                      <ActivityRow
                        key={item.id}
                        item={item}
                        onOpenIssue={
                          item.issue_id
                            ? () =>
                                router.push(
                                  wsPaths.issueDetail(item.issue_id as string),
                                )
                            : undefined
                        }
                        onOpenAgent={
                          item.agent_id
                            ? () =>
                                router.push(
                                  wsPaths.agentDetail(item.agent_id as string),
                                )
                            : undefined
                        }
                      />
                    ))}
                  </div>
                ) : (
                  <div className="py-10 text-center text-muted-foreground">
                    No autonomous activity recorded yet.
                  </div>
                )}
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="decisions" className="space-y-3">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <ShieldCheck className="size-4" />
                  Team planner decision history
                </CardTitle>
                <CardDescription>
                  Each issue revision and manual replan is cached as a durable
                  TeamPlan decision.
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-2">
                {data.decisions.length > 0 ? (
                  data.decisions.map((decision) => (
                    <DecisionCard key={decision.id} decision={decision} />
                  ))
                ) : (
                  <div className="py-8 text-center text-muted-foreground">
                    No team planner decisions recorded yet.
                  </div>
                )}
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
