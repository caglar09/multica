"use client";

import type {
  Project,
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
  AutonomousRoleRuntimeAssignment,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";

import { LiveSquadWidget } from "./widgets/live-squad-widget";
import { DecisionGateWidget } from "./widgets/decision-gate-widget";
import { IssuesVelocityWidget } from "./widgets/issues-velocity-widget";
import {
  ActivityTimelineWidget,
  WorkflowQueueWidget,
  WorkWaitingWidget,
} from "./widgets/execution-overview-widgets";
import { ProjectBootstrapFlow } from "./project-bootstrap-flow";

export interface ProjectCockpitViewProps {
  project: Project;
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[];
  canControl?: boolean;
  onNavigateTab: (tab: "cockpit" | "issues" | "report") => void;
  onOpenLeaderChat: () => void;
  onConfirmTeam?: (assignments: AutonomousRoleRuntimeAssignment[]) => void;
  onReplanTeam?: () => void;
  isConfirmingTeam?: boolean;
  onStartProjectPlanning?: () => void;
  isStartingProjectPlanning?: boolean;
  onApproveChange?: (id: string) => void;
  onRejectChange?: (id: string) => void;
  onResolveEscalation?: (id: string) => void;
  isApproving?: boolean;
  isRejecting?: boolean;
  onRestartWorkflow?: () => void;
  onResumeExecution?: () => void;
  onRetryAction?: (actionId: string) => void;
  onRerunIssue?: (issueId: string, taskId?: string) => void;
  isRepairing?: boolean;
  isRetryingAction?: boolean;
  isRerunningIssue?: boolean;
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
  onReplanTeam,
  isConfirmingTeam,
  onStartProjectPlanning,
  isStartingProjectPlanning,
  onApproveChange,
  onRejectChange,
  onResolveEscalation,
  isApproving,
  isRejecting,
  onRestartWorkflow,
  onResumeExecution,
  onRetryAction,
  onRerunIssue,
  isRepairing,
  isRetryingAction,
  isRerunningIssue,
  className,
}: ProjectCockpitViewProps) {
  return (
    <div className={cn("flex-1 overflow-y-auto p-4 md:p-6 space-y-6", className)}>
      {snapshot?.enabled && (
        <WorkWaitingWidget
          snapshot={snapshot}
          canControl={canControl}
          onRestartWorkflow={onRestartWorkflow}
          onResumeExecution={onResumeExecution}
          onReplan={onReplanTeam}
          onRetryAction={onRetryAction}
          onRerunIssue={onRerunIssue}
          pending={isRepairing || isRetryingAction || isRerunningIssue}
        />
      )}

      {snapshot?.enabled && (
        <ProjectBootstrapFlow
          snapshot={snapshot}
          canControl={canControl}
          isConfirmingTeam={isConfirmingTeam}
          onConfirmTeam={onConfirmTeam}
          onReplanTeam={onReplanTeam}
          onOpenLeaderChat={onOpenLeaderChat}
          onStartProjectPlanning={onStartProjectPlanning}
          isStartingProjectPlanning={isStartingProjectPlanning}
        />
      )}

      <div className="grid gap-4 md:grid-cols-2 lg:gap-6">
        <LiveSquadWidget
          snapshot={snapshot}
          className="min-h-[260px]"
        />

        <WorkflowQueueWidget snapshot={snapshot} className="min-h-[240px]" />

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

        <ActivityTimelineWidget snapshot={snapshot} className="min-h-[240px]" />

        <IssuesVelocityWidget
          project={project}
          onViewIssues={() => onNavigateTab("issues")}
          className="min-h-[240px] md:col-span-2"
        />
      </div>
    </div>
  );
}
