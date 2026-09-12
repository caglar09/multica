"use client";

import type {
  Project,
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  AutonomousRoleRuntimeAssignment,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";

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
  return (
    <div className={cn("flex-1 overflow-y-auto p-4 md:p-6 space-y-6", className)}>
      {/* 1. Squad Bootstrapping & Proposal (When team draft is pending confirmation) */}
      {snapshot?.draft && onConfirmTeam && (
        <SquadBootstrappingWidget
          snapshot={snapshot}
          canControl={canControl}
          isConfirming={isConfirmingTeam}
          onConfirmTeam={onConfirmTeam}
        />
      )}

      {/* 2. Responsive 6-Widget Cockpit Grid */}
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
