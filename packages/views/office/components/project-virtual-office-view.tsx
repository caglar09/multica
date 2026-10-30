import { useState, useMemo, useCallback } from "react";
import { cn } from "@multica/ui/lib/utils";
import { buildVirtualOfficeScene } from "../spatial-mapper";
import { IsometricOfficeCanvas } from "./isometric-office-canvas";
import { OfficeInspectorDrawer } from "./office-inspector-drawer";
import type {
  VirtualOfficeViewProps,
  OfficeZoneKey,
  OfficeAgentDesk,
  OfficeReplayEvent,
} from "../types";

export function ProjectVirtualOfficeView({
  snapshot,
  changeRequests,
  agents = [],
  issues = [],
  canControl = false,
  onApprovePlan,
  onApproveChange,
  onRejectChange,
  onResolveEscalation,
  onOpenLeaderChat,
  className,
}: VirtualOfficeViewProps) {
  const [selectedStationKey, setSelectedStationKey] = useState<OfficeZoneKey | null>(null);
  const [selectedDesk, setSelectedDesk] = useState<OfficeAgentDesk | null>(null);
  const [activeReplay, setActiveReplay] = useState<OfficeReplayEvent | null>(null);
  const [isDrawerOpen, setIsDrawerOpen] = useState(true);
  const [isFullscreen, setIsFullscreen] = useState(false);

  // Compute spatial office scene
  const scene = useMemo(
    () =>
      buildVirtualOfficeScene({
        snapshot,
        changeRequests,
        agents,
        issues,
      }),
    [snapshot, changeRequests, agents, issues],
  );

  // Replay packet flow on map with 3.5s auto-clear
  const handleReplayEvent = useCallback((event: OfficeReplayEvent) => {
    setActiveReplay(event);
    setSelectedStationKey(event.toStation);
    const timer = setTimeout(() => {
      setActiveReplay((current) => (current?.id === event.id ? null : current));
    }, 3800);
    return () => clearTimeout(timer);
  }, []);

  const handlePlayRecentFlow = useCallback(() => {
    if (scene.recentEvents.length > 0) {
      handleReplayEvent(scene.recentEvents[0]!);
    }
  }, [scene.recentEvents, handleReplayEvent]);

  const handleToggleFullscreen = useCallback(() => {
    setIsFullscreen((f) => !f);
  }, []);

  return (
    <div
      className={cn(
        "relative flex size-full min-h-0 flex-1 overflow-hidden bg-slate-950",
        isFullscreen && "fixed inset-0 z-50",
        className,
      )}
    >
      {/* 2.5D Isometric Canvas Main Area */}
      <IsometricOfficeCanvas
        scene={scene}
        activeReplay={activeReplay}
        selectedStationKey={selectedStationKey}
        selectedDesk={selectedDesk}
        isFullscreen={isFullscreen}
        onSelectStation={(key) => {
          setSelectedStationKey(key);
          setIsDrawerOpen(true);
        }}
        onSelectDesk={(desk) => {
          setSelectedDesk(desk);
          setSelectedStationKey(desk.stationKey);
          setIsDrawerOpen(true);
        }}
        onPlayRecentFlow={handlePlayRecentFlow}
        onToggleFullscreen={handleToggleFullscreen}
        className="flex-1"
      />

      {/* Right Spectator & Inspector Drawer */}
      <OfficeInspectorDrawer
        scene={scene}
        snapshot={snapshot}
        changeRequests={changeRequests}
        selectedDesk={selectedDesk}
        selectedStationKey={selectedStationKey}
        activeReplay={activeReplay}
        canControl={canControl}
        isOpen={isDrawerOpen}
        onClose={() => setIsDrawerOpen(false)}
        onReplayEvent={handleReplayEvent}
        onApprovePlan={onApprovePlan}
        onApproveChange={onApproveChange}
        onRejectChange={onRejectChange}
        onResolveEscalation={onResolveEscalation}
        onOpenLeaderChat={onOpenLeaderChat}
      />
    </div>
  );
}
