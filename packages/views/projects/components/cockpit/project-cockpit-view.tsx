"use client";

import { Play, Pause, RotateCcw, Sparkles } from "lucide-react";

import type {
  Project,
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  AutonomousRoleRuntimeAssignment,
} from "@multica/core/types";
import {
  usePauseAutonomousProject,
  useResumeAutonomousProject,
  useReplanAutonomousProject,
} from "@multica/core/projects";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { toast } from "sonner";

import { useT } from "../../../i18n";
import { LiveSquadWidget } from "./widgets/live-squad-widget";
import { ExecutionLoopWidget } from "./widgets/execution-loop-widget";
import { DecisionGateWidget } from "./widgets/decision-gate-widget";
import { IssuesVelocityWidget } from "./widgets/issues-velocity-widget";
import { TelemetryCostWidget } from "./widgets/telemetry-cost-widget";
import { BrainQualityWidget } from "./widgets/brain-quality-widget";
import { SquadBootstrappingWidget } from "./widgets/squad-bootstrapping-widget";

export interface ProjectCockpitViewProps {
  project: Project;
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[];
  canControl?: boolean;
  onNavigateTab: (tab: "cockpit" | "issues" | "autonomous" | "report") => void;
  onOpenLeaderChat: () => void;
  onConfirmTeam?: (assignments: AutonomousRoleRuntimeAssignment[]) => void;
  isConfirmingTeam?: boolean;
  onApproveChange?: (id: string) => void;
  onRejectChange?: (id: string) => void;
  onResolveEscalation?: (id: string) => void;
  isApproving?: boolean;
  isRejecting?: boolean;
  className?: string;
}

export function ProjectCockpitView({
  project,
  snapshot,
  changeRequests = [],
  canControl = false,
  onNavigateTab,
  onOpenLeaderChat,
  onConfirmTeam,
  isConfirmingTeam,
  onApproveChange,
  onRejectChange,
  onResolveEscalation,
  isApproving,
  isRejecting,
  className,
}: ProjectCockpitViewProps) {
  const { t } = useT("projects");

  const pauseMutation = usePauseAutonomousProject();
  const resumeMutation = useResumeAutonomousProject();
  const replanMutation = useReplanAutonomousProject();

  const isPaused = Boolean(snapshot?.control.paused);
  const isAttention = Boolean(
    snapshot?.health.status === "attention" ||
      changeRequests.some((c) => c.state === "approval_required") ||
      (snapshot?.escalations && snapshot.escalations.length > 0),
  );

  const isExecuting = Boolean(
    snapshot?.health.active_workflows && snapshot.health.active_workflows > 0,
  );

  const handleTogglePause = () => {
    if (isPaused) {
      resumeMutation.mutate(project.id, {
        onSuccess: () => toast.success(t(($) => $.cockpit.toast_resume_success)),
        onError: () => toast.error(t(($) => $.cockpit.toast_resume_error)),
      });
    } else {
      pauseMutation.mutate(project.id, {
        onSuccess: () => toast.success(t(($) => $.cockpit.toast_pause_success)),
        onError: () => toast.error(t(($) => $.cockpit.toast_pause_error)),
      });
    }
  };

  const handleReplan = () => {
    replanMutation.mutate(project.id, {
      onSuccess: () => toast.success(t(($) => $.cockpit.toast_replan_success)),
      onError: () => toast.error(t(($) => $.cockpit.toast_replan_error)),
    });
  };

  return (
    <div className={cn("flex-1 overflow-y-auto p-4 md:p-6 space-y-6", className)}>
      {/* 1. Quick Control Command Strip */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between rounded-xl border bg-muted/20 p-3.5 backdrop-blur-xs">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <span
              className={cn(
                "relative flex size-2.5 rounded-full",
                isExecuting
                  ? "bg-emerald-500 animate-pulse"
                  : isPaused
                    ? "bg-amber-500"
                    : isAttention
                      ? "bg-destructive"
                      : "bg-muted-foreground/50",
              )}
            />
            <span className="text-xs font-semibold text-foreground">
              {isExecuting
                ? t(($) => $.cockpit.health_running)
                : isPaused
                  ? t(($) => $.cockpit.health_paused)
                  : isAttention
                    ? t(($) => $.cockpit.health_attention)
                    : t(($) => $.cockpit.health_idle)}
            </span>
          </div>

          <div className="hidden h-3.5 w-px bg-border sm:block" />

          <span className="hidden text-[11px] font-mono text-muted-foreground sm:inline">
            {t(($) => $.cockpit.active_agents_count, {
              count: snapshot?.team?.members.filter((m) => m.active).length ?? 0,
            })}
          </span>
        </div>

        {/* Quick Action Controls */}
        <div className="flex items-center gap-2">
          {canControl && (
            <>
              <Button
                variant={isPaused ? "default" : "outline"}
                size="sm"
                onClick={handleTogglePause}
                disabled={pauseMutation.isPending || resumeMutation.isPending}
                className="text-xs h-7 px-2.5"
              >
                {isPaused ? (
                  <>
                    <Play className="size-3 mr-1 text-emerald-500 fill-emerald-500" />
                    {t(($) => $.cockpit.resume_execution)}
                  </>
                ) : (
                  <>
                    <Pause className="size-3 mr-1 text-amber-500" />
                    {t(($) => $.cockpit.pause_execution)}
                  </>
                )}
              </Button>

              <Button
                variant="outline"
                size="sm"
                onClick={handleReplan}
                disabled={replanMutation.isPending}
                className="text-xs h-7 px-2.5"
              >
                <RotateCcw className="size-3 mr-1" />
                {t(($) => $.cockpit.replan_project)}
              </Button>
            </>
          )}

          <Button
            variant="secondary"
            size="sm"
            onClick={onOpenLeaderChat}
            className="text-xs h-7 px-2.5 gap-1 font-medium bg-primary/10 hover:bg-primary/20 text-primary border border-primary/20"
          >
            <Sparkles className="size-3" />
            {t(($) => $.cockpit.ask_pm)}
          </Button>
        </div>
      </div>

      {/* 2. Squad Bootstrapping & Proposal (When team draft is pending confirmation) */}
      {snapshot?.draft && onConfirmTeam && (
        <SquadBootstrappingWidget
          snapshot={snapshot}
          canControl={canControl}
          isConfirming={isConfirmingTeam}
          onConfirmTeam={onConfirmTeam}
          onReplanTeam={canControl ? handleReplan : undefined}
        />
      )}

      {/* 3. Responsive 6-Widget Cockpit Grid */}
      <div className="grid gap-4 md:grid-cols-2 lg:gap-6">
        {/* Widget 1: Live Squad */}
        <LiveSquadWidget
          snapshot={snapshot}
          onConfigureSquad={() => onNavigateTab("autonomous")}
          className="min-h-[260px]"
        />

        {/* Widget 2: Execution Loop & Feed */}
        <ExecutionLoopWidget
          snapshot={snapshot}
          onViewActivity={() => onNavigateTab("autonomous")}
          className="min-h-[260px]"
        />

        {/* Widget 3: Decision Gate (Human-in-the-loop) */}
        <DecisionGateWidget
          snapshot={snapshot}
          changeRequests={changeRequests}
          canControl={canControl}
          onApproveChange={onApproveChange}
          onRejectChange={onRejectChange}
          onResolveEscalation={onResolveEscalation}
          onOpenLeaderChat={onOpenLeaderChat}
          isApproving={isApproving}
          isRejecting={isRejecting}
          className="min-h-[240px]"
        />

        {/* Widget 4: Issues Velocity */}
        <IssuesVelocityWidget
          project={project}
          onViewIssues={() => onNavigateTab("issues")}
          className="min-h-[240px]"
        />

        {/* Widget 5: Telemetry & Cost */}
        <TelemetryCostWidget
          budget={snapshot?.budget}
          onViewReports={() => onNavigateTab("report")}
          className="min-h-[220px]"
        />

        {/* Widget 6: Brain & Quality */}
        <BrainQualityWidget
          snapshot={snapshot}
          onViewStudio={() => onNavigateTab("autonomous")}
          className="min-h-[220px]"
        />
      </div>
    </div>
  );
}
