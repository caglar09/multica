"use client";

import {
  BrainCircuit,
  CheckCircle2,
  Circle,
  LoaderCircle,
  MessageSquare,
  Play,
  UsersRound,
} from "lucide-react";
import type { ReactNode } from "react";

import type {
  AutonomousProjectSnapshot,
  AutonomousRoleRuntimeAssignment,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";

import { useT } from "../../../i18n";
import { SquadBootstrappingWidget } from "./widgets/squad-bootstrapping-widget";

export interface ProjectBootstrapFlowProps {
  snapshot?: AutonomousProjectSnapshot | null;
  canControl: boolean;
  onConfirmTeam?: (assignments: AutonomousRoleRuntimeAssignment[]) => void;
  onReplanTeam?: () => void;
  isConfirmingTeam?: boolean;
  onOpenLeaderChat: () => void;
  onStartProjectPlanning?: () => void;
  isStartingProjectPlanning?: boolean;
}

export function ProjectBootstrapFlow({
  snapshot,
  canControl,
  onConfirmTeam,
  onReplanTeam,
  isConfirmingTeam,
  onOpenLeaderChat,
  onStartProjectPlanning,
  isStartingProjectPlanning,
}: ProjectBootstrapFlowProps) {
  const { t } = useT("projects");
  const draft = snapshot?.draft;
  const teamProvisioned = Boolean(snapshot?.team);
  const planningStarted = Boolean(draft?.continuation_started_at);
  const planCreated = Boolean(snapshot?.plan?.nodes?.length);
  const workersStarted = Boolean(
    snapshot?.health.active_workflows ||
      snapshot?.workflows?.length ||
      snapshot?.team?.members.some((member) => member.current_task_id),
  );
  const teamApproved = draft?.status === "provisioning" || teamProvisioned;
  const teamPlanned = Boolean(draft || teamProvisioned);

  const stages = [
    { label: t(($) => $.cockpit.setup_team_planning), done: teamPlanned, active: !teamPlanned },
    { label: t(($) => $.cockpit.setup_team_planned), done: teamApproved, active: draft?.status === "awaiting_configuration" },
    { label: t(($) => $.cockpit.setup_team_approved), done: teamProvisioned, active: draft?.status === "provisioning" },
    { label: t(($) => $.cockpit.setup_team_provisioned), done: teamProvisioned, active: teamProvisioned && !planningStarted && !planCreated },
    { label: t(($) => $.cockpit.setup_pm_thinking), done: planCreated, active: teamProvisioned && !planningStarted && !planCreated },
    { label: t(($) => $.cockpit.setup_workers_started), done: workersStarted, active: planningStarted && !workersStarted },
  ];

  const currentStage = stages.find((stage) => stage.active) ?? stages.at(-1)!;

  return (
    <section className="rounded-xl border bg-card shadow-xs" aria-labelledby="project-setup-title">
      <div className="flex flex-col gap-3 border-b p-4 md:flex-row md:items-start md:justify-between md:p-5">
        <div className="flex gap-3">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <BrainCircuit className="size-4" />
          </div>
          <div>
            <h2 id="project-setup-title" className="text-sm font-semibold text-foreground">
              {t(($) => $.cockpit.setup_title)}
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.cockpit.setup_description)}
            </p>
          </div>
        </div>
        {snapshot?.enabled && (
          <div className="flex flex-wrap gap-2 self-start">
            <Button variant="outline" size="sm" className="text-xs" onClick={onOpenLeaderChat}>
              <MessageSquare className="size-3.5" />
              {t(($) => $.cockpit.ask_pm)}
            </Button>
          </div>
        )}
      </div>

      <ol className="grid grid-cols-2 gap-2 p-4 sm:grid-cols-3 lg:grid-cols-6" aria-label={t(($) => $.cockpit.setup_title)}>
        {stages.map((stage) => (
          <li
            key={stage.label}
            className={cn(
              "min-h-20 rounded-lg border p-3",
              stage.done
                ? "border-emerald-500/30 bg-emerald-500/5"
                : stage.active
                  ? "border-primary/40 bg-primary/5 ring-1 ring-primary/10"
                  : "border-border/50 bg-muted/20 text-muted-foreground",
            )}
          >
            <div className="flex items-center gap-2 text-xs font-medium">
              {stage.done ? (
                <CheckCircle2 className="size-4 shrink-0 text-emerald-500" aria-hidden="true" />
              ) : stage.active ? (
                <LoaderCircle className="size-4 shrink-0 animate-spin text-primary" aria-hidden="true" />
              ) : (
                <Circle className="size-4 shrink-0 text-muted-foreground/50" aria-hidden="true" />
              )}
              <span>{stage.label}</span>
            </div>
          </li>
        ))}
      </ol>

      <div className="border-t bg-muted/10 p-4 md:p-5">
        {draft?.status === "awaiting_configuration" && onConfirmTeam ? (
          <SquadBootstrappingWidget
            snapshot={snapshot!}
            canControl={canControl}
            isConfirming={isConfirmingTeam}
            onConfirmTeam={onConfirmTeam}
            onReplanTeam={onReplanTeam}
            className="border-0 bg-transparent p-0 shadow-none"
          />
        ) : draft?.status === "provisioning" ? (
          <FlowNotice icon={<LoaderCircle className="size-4 animate-spin" />} title={t(($) => $.cockpit.setup_provisioning_title)} detail={t(($) => $.cockpit.setup_provisioning_description)} />
        ) : teamProvisioned && !planningStarted && !planCreated ? (
          <div className="flex flex-col gap-4 rounded-lg border border-primary/20 bg-primary/[0.03] p-4 md:flex-row md:items-center md:justify-between">
            <div className="flex gap-3">
              <BrainCircuit className="mt-0.5 size-4 shrink-0 text-primary" />
              <div>
                <h3 className="text-sm font-medium text-foreground">{t(($) => $.cockpit.setup_pm_title)}</h3>
                <p className="mt-1 max-w-2xl text-xs leading-relaxed text-muted-foreground">{t(($) => $.cockpit.setup_pm_description)}</p>
              </div>
            </div>
            <div className="flex shrink-0 flex-wrap gap-2">
              <Button variant="outline" size="sm" className="text-xs" onClick={onOpenLeaderChat}>
                <MessageSquare className="size-3.5" />
                {t(($) => $.cockpit.setup_discuss_scope)}
              </Button>
              {canControl && onStartProjectPlanning && (
                <Button size="sm" className="text-xs" onClick={onStartProjectPlanning} disabled={isStartingProjectPlanning}>
                  <Play className="size-3.5" />
                  {t(($) => $.cockpit.setup_start_planning)}
                </Button>
              )}
            </div>
          </div>
        ) : planningStarted && !planCreated ? (
          <FlowNotice icon={<LoaderCircle className="size-4 animate-spin" />} title={t(($) => $.cockpit.setup_planning_title)} detail={t(($) => $.cockpit.setup_planning_description)} />
        ) : workersStarted ? (
          <FlowNotice icon={<UsersRound className="size-4" />} title={t(($) => $.cockpit.setup_workers_title)} detail={t(($) => $.cockpit.setup_workers_description)} />
        ) : (
          <FlowNotice icon={<LoaderCircle className="size-4 animate-spin" />} title={currentStage.label} detail={t(($) => $.cockpit.setup_waiting)} />
        )}
      </div>
    </section>
  );
}

function FlowNotice({ icon, title, detail }: { icon: ReactNode; title: string; detail: string }) {
  return (
    <div className="flex gap-3 rounded-lg border bg-background p-4 text-muted-foreground">
      <span className="mt-0.5 text-primary" aria-hidden="true">{icon}</span>
      <div>
        <h3 className="text-sm font-medium text-foreground">{title}</h3>
        <p className="mt-1 text-xs leading-relaxed">{detail}</p>
      </div>
    </div>
  );
}
