"use client";

import { useCallback, useMemo, useState } from "react";
import { useAuthStore } from "@multica/core/auth";
import { useRerunAutonomousIssue, useRetryAutonomousAction } from "@multica/core/projects";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { OfficeCommanderDock } from "./office-commander-dock";
import { IsometricOfficeCanvas } from "./isometric-office-canvas";
import { OfficeInspectorDrawer } from "./office-inspector-drawer";
import { useOfficeConversation } from "../use-office-conversation";
import { useOfficeLiveData } from "../use-office-live-data";
import { useOfficeBlockerDismissals } from "../use-office-blocker-dismissals";
import type {
  VirtualOfficeViewProps,
  OfficeZoneKey,
  OfficeAgentDesk,
  OfficeReplayEvent,
  OfficeBlocker,
} from "../types";

export function ProjectVirtualOfficeView({
  projectId,
  snapshot,
  changeRequests,
  leaderSessionId,
  leaderAgentId,
  canControl = false,
  onApprovePlan,
  onApproveChange,
  onRejectChange,
  onResolveEscalation,
  className,
}: VirtualOfficeViewProps) {
  const { t } = useT("projects");
  const { scene, isLoading, isPartial } = useOfficeLiveData(projectId ?? undefined);
  const userId = useAuthStore((state) => state.user?.id ?? null);
  const [dismissedBlockerIds, dismissBlockers] = useOfficeBlockerDismissals(userId, projectId);
  const visibleScene = useMemo(() => {
    const dismissedFailures = scene.blockers.filter(
      (blocker) => blocker.kind === "failed_run" && dismissedBlockerIds.has(blocker.id),
    ).length;
    return {
      ...scene,
      blockers: scene.blockers.filter((blocker) => !dismissedBlockerIds.has(blocker.id)),
      stats: { ...scene.stats, failed: Math.max(0, scene.stats.failed - dismissedFailures) },
    };
  }, [dismissedBlockerIds, scene]);
  const [selectedStationKey, setSelectedStationKey] = useState<OfficeZoneKey | null>(null);
  const [selectedDeskId, setSelectedDeskId] = useState<string | null>(null);
  const [activeReplay, setActiveReplay] = useState<OfficeReplayEvent | null>(null);
  const [isDrawerOpen, setIsDrawerOpen] = useState(true);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const rerunIssue = useRerunAutonomousIssue();
  const retryAction = useRetryAutonomousAction();

  const selectedDesk = scene.desks.find((desk) => desk.id === selectedDeskId) ?? null;
  const conversationDesk = selectedDesk ?? scene.desks.find((desk) => desk.isLeader) ?? scene.desks[0] ?? null;
  const conversation = useOfficeConversation({
    projectId,
    desk: conversationDesk,
    leaderSessionId,
    leaderAgentId,
  });
  const isHandlingBlocker = rerunIssue.isPending || retryAction.isPending;

  const handleReplayEvent = useCallback((event: OfficeReplayEvent) => {
    setActiveReplay(event);
    setSelectedStationKey(event.toStation ?? null);
    const timer = setTimeout(() => {
      setActiveReplay((current) => (current?.id === event.id ? null : current));
    }, 3800);
    return () => clearTimeout(timer);
  }, []);

  const handlePlayRecentFlow = useCallback(() => {
    const event = scene.recentEvents[0];
    if (event) handleReplayEvent(event);
  }, [scene.recentEvents, handleReplayEvent]);

  const handleBlockerAction = useCallback((blocker: OfficeBlocker) => {
    if (!canControl || !projectId) return;
    switch (blocker.action.kind) {
      case "retry_run":
        if (blocker.issueId) {
          rerunIssue.mutate({ projectId, issueId: blocker.issueId, taskId: blocker.action.targetId });
        }
        break;
      case "retry_action":
        retryAction.mutate({ projectId, actionId: blocker.action.targetId });
        break;
      case "resolve_escalation":
        onResolveEscalation?.(blocker.action.targetId);
        break;
      case "approve_plan":
        onApprovePlan?.();
        break;
      case "open_issue":
      case "none":
        break;
    }
  }, [canControl, onApprovePlan, onResolveEscalation, projectId, rerunIssue, retryAction]);

  return (
    <div
      className={cn(
        "relative flex size-full min-h-0 flex-1 overflow-hidden bg-slate-950",
        isFullscreen && "fixed inset-0 z-50",
        className,
      )}
    >
      <div className="relative flex min-h-0 min-w-0 flex-1 flex-col">
        <IsometricOfficeCanvas
          scene={visibleScene}
          activeReplay={activeReplay}
          selectedDesk={selectedDesk}
          isFullscreen={isFullscreen}
          onSelectStation={(key) => {
            setSelectedStationKey(key);
            setIsDrawerOpen(true);
          }}
          onSelectDesk={(desk: OfficeAgentDesk | null) => {
            setSelectedDeskId(desk?.id ?? null);
            if (desk) setSelectedStationKey(desk.stationKey);
            setIsDrawerOpen(true);
          }}
          onPlayRecentFlow={handlePlayRecentFlow}
          onToggleFullscreen={() => setIsFullscreen((fullscreen) => !fullscreen)}
          className="min-h-0 min-w-0 flex-1"
        />

        {isPartial && !isLoading ? (
          <p
            role="status"
            className="absolute left-3 top-3 z-20 max-w-[calc(100%-1.5rem)] rounded-md border border-warning/30 bg-background/95 px-3 py-2 text-caption text-muted-foreground shadow-sm md:left-4"
          >
            {t(($) => $.office.partial_data)}
          </p>
        ) : null}

        <OfficeCommanderDock
          projectId={projectId}
          desks={scene.desks}
          selectedDesk={conversationDesk}
          onSelectDesk={(desk) => setSelectedDeskId(desk?.id ?? null)}
          conversation={conversation}
          className="relative z-40 mb-3 md:z-20"
        />
      </div>

      <OfficeInspectorDrawer
        scene={visibleScene}
        snapshot={snapshot}
        changeRequests={changeRequests}
        selectedDesk={selectedDesk}
        selectedStationKey={selectedStationKey}
        activeReplay={activeReplay}
        canControl={canControl && !isHandlingBlocker}
        isOpen={isDrawerOpen}
        onClose={() => setIsDrawerOpen(false)}
        onReplayEvent={handleReplayEvent}
        onApprovePlan={onApprovePlan}
        onApproveChange={onApproveChange}
        onRejectChange={onRejectChange}
        onResolveEscalation={onResolveEscalation}
        onBlockerAction={handleBlockerAction}
        onDismissFailedBlockers={dismissBlockers}
      />
    </div>
  );
}
