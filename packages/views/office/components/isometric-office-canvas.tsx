import { useRef, useCallback, useEffect, useId } from "react";
import {
  ShieldAlert,
  Play,
  Maximize2,
  Minimize2,
  ZoomIn,
  ZoomOut,
  RotateCcw,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n";
import type {
  VirtualOfficeSceneData,
  OfficeZoneKey,
  OfficeAgentDesk,
  OfficeReplayEvent,
} from "../types";
import { PacketFlowLayer } from "./packet-flow-layer";
import { AgentDeskHud } from "./agent-desk-hud";

interface IsometricOfficeCanvasProps {
  scene: VirtualOfficeSceneData;
  activeReplay?: OfficeReplayEvent | null;
  selectedDesk?: OfficeAgentDesk | null;
  isFullscreen?: boolean;
  onSelectStation?: (key: OfficeZoneKey) => void;
  onSelectDesk?: (desk: OfficeAgentDesk | null) => void;
  onPlayRecentFlow?: () => void;
  onToggleFullscreen?: () => void;
  className?: string;
}

export function IsometricOfficeCanvas({
  scene,
  activeReplay,
  selectedDesk,
  isFullscreen,
  onSelectStation,
  onSelectDesk,
  onPlayRecentFlow,
  onToggleFullscreen,
  className,
}: IsometricOfficeCanvasProps) {
  const { t } = useT("projects");
  const canvasId = useId().replaceAll(":", "");
  const viewRef = useRef({ zoom: 1, x: 0, y: 0 });
  const isDraggingRef = useRef(false);
  const dragStartRef = useRef({ x: 0, y: 0 });
  const canvasRef = useRef<HTMLDivElement>(null);
  const worldRef = useRef<HTMLDivElement>(null);

  const updateTransform = useCallback(() => {
    const { zoom, x, y } = viewRef.current;
    if (worldRef.current) {
      worldRef.current.style.transform = `translate(${x}px, ${y}px) scale(${zoom})`;
    }
  }, []);

  const changeZoom = useCallback((delta: number) => {
    viewRef.current.zoom = Math.min(Math.max(viewRef.current.zoom + delta, 0.4), 2.5);
    updateTransform();
  }, [updateTransform]);

  // Mouse drag to pan
  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    // Only drag with primary mouse button
    if (e.button !== 0 || (e.target instanceof Element && e.target.closest("button, [role='button']"))) return;
    isDraggingRef.current = true;
    worldRef.current?.classList.add("transition-none");
    dragStartRef.current = {
      x: e.clientX - viewRef.current.x,
      y: e.clientY - viewRef.current.y,
    };
  }, []);

  const handleMouseMove = useCallback((e: React.MouseEvent) => {
    if (!isDraggingRef.current) return;
    viewRef.current.x = e.clientX - dragStartRef.current.x;
    viewRef.current.y = e.clientY - dragStartRef.current.y;
    updateTransform();
  }, [updateTransform]);

  const handleMouseUp = useCallback(() => {
    isDraggingRef.current = false;
    worldRef.current?.classList.remove("transition-none");
  }, []);

  const handleResetView = useCallback(() => {
    viewRef.current = { zoom: 1, x: 0, y: 0 };
    updateTransform();
  }, [updateTransform]);

  // Trackpad 2-finger gesture & mouse wheel handler
  // Supports pinch-to-zoom (ctrlKey) and smooth wheel zoom, plus trackpad pan
  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;

    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      e.stopPropagation();

      if (e.ctrlKey) {
        // Trackpad pinch-to-zoom
        const factor = 0.015;
        const delta = -e.deltaY * factor;
        changeZoom(delta);
      } else if (e.shiftKey || Math.abs(e.deltaX) > 0) {
        // Trackpad 2-finger horizontal / vertical pan
        viewRef.current.x -= e.deltaX;
        viewRef.current.y -= e.deltaY;
        updateTransform();
      } else {
        // Standard mouse wheel: smooth zoom in / out
        const factor = 0.0018;
        const delta = -e.deltaY * factor;
        changeZoom(delta);
      }
    };

    el.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      el.removeEventListener("wheel", onWheel);
    };
  }, [changeZoom, updateTransform]);

  const { desks, flowEdges, stats } = scene;

  return (
    <div
      role="region"
      aria-label={t(($) => $.office.title)}
      className={cn(
        "relative flex size-full min-w-0 flex-col overflow-hidden bg-background font-sans text-foreground",
        isFullscreen && "fixed inset-0 z-50",
        className,
      )}
    >
      {/* Top HUD Control Bar */}
      <div className="z-10 flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-border bg-background/95 px-4 py-2.5 backdrop-blur-md">
        {/* Left: Quick Stats Summary */}
        <div role="group" aria-label={t(($) => $.office.stats.health_summary)} className="flex min-w-0 max-w-full flex-1 items-center gap-2 overflow-x-auto text-caption">
          <Badge
            variant="outline"
            className="gap-1.5 border-info/30 bg-info/10 py-1 text-info"
          >
            <span className="size-1.5 rounded-full bg-info motion-safe:animate-pulse motion-reduce:animate-none" />
            <span>{t(($) => $.office.stats.active_agents)}:</span>
            <span className="font-bold">{stats.activeAgents}</span>
          </Badge>

          <Badge
            variant="outline"
            className="gap-1.5 border-primary/30 bg-primary/10 py-1 text-primary"
          >
            <span className="size-1.5 rounded-full bg-primary" />
            <span>{t(($) => $.office.stats.running_tasks)}:</span>
            <span className="font-bold">{stats.runningTasks}</span>
          </Badge>

          <Badge
            variant="outline"
            className="gap-1.5 border-success/30 bg-success/10 py-1 text-success"
          >
            <span className="size-1.5 rounded-full bg-success" />
            <span>{t(($) => $.office.stats.completed_tasks)}:</span>
            <span className="font-bold">{stats.completedTasks}</span>
          </Badge>

          <Badge variant="outline" className="gap-1.5 border-destructive/30 bg-destructive/10 py-1 text-destructive">
            <ShieldAlert className="size-3" />
            <span>{t(($) => $.office.stats.blocked)}:</span>
            <span className="font-bold">{stats.blocked}</span>
          </Badge>

          <Badge variant="outline" className="gap-1.5 border-destructive/30 bg-destructive/10 py-1 text-destructive">
            <span>{t(($) => $.office.stats.failed)}:</span>
            <span className="font-bold">{stats.failed}</span>
          </Badge>

          <Badge variant="outline" className="gap-1.5 border-warning/30 bg-warning/10 py-1 text-warning">
            <span>{t(($) => $.office.stats.waiting)}:</span>
            <span className="font-bold">{stats.waiting}</span>
          </Badge>

          <Badge variant="outline" className="gap-1.5 border-info/30 bg-info/10 py-1 text-info">
            <span>{t(($) => $.office.stats.resumable)}:</span>
            <span className="font-bold">{stats.resumable}</span>
          </Badge>

          {scene.pendingApprovalsCount > 0 && (
            <Badge
              variant="outline"
              className="gap-1.5 border-warning/30 bg-warning/10 py-1 text-warning motion-safe:animate-pulse motion-reduce:animate-none"
            >
              <ShieldAlert className="size-3" />
              <span>{t(($) => $.office.stats.pending_approvals)}:</span>
              <span className="font-bold">{stats.pendingApprovals}</span>
            </Badge>
          )}
        </div>

        {/* Right: Controls & Actions */}
        <div className="flex items-center gap-1.5">
          {onPlayRecentFlow && (
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={onPlayRecentFlow}
              className="h-8 gap-1.5 text-caption font-medium"
            >
              <Play className="size-3 fill-warning text-warning" />
              <span>{t(($) => $.office.play_flow)}</span>
            </Button>
          )}

          {/* Zoom controls */}
          <div className="flex items-center rounded-md border border-border bg-muted p-0.5">
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    aria-label={t(($) => $.office.controls.zoom_in)}
                    onClick={() => changeZoom(0.15)}
                    className="size-8 text-muted-foreground hover:text-foreground"
                  >
                    <ZoomIn className="size-3.5" />
                  </Button>
                }
              />
              <TooltipContent side="bottom">{t(($) => $.office.controls.zoom_in)}</TooltipContent>
            </Tooltip>

            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    aria-label={t(($) => $.office.controls.zoom_out)}
                    onClick={() => changeZoom(-0.15)}
                    className="size-8 text-muted-foreground hover:text-foreground"
                  >
                    <ZoomOut className="size-3.5" />
                  </Button>
                }
              />
              <TooltipContent side="bottom">{t(($) => $.office.controls.zoom_out)}</TooltipContent>
            </Tooltip>

            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    onClick={handleResetView}
                    aria-label={t(($) => $.office.controls.reset_view)}
                    className="size-8 text-muted-foreground hover:text-foreground"
                  >
                    <RotateCcw className="size-3" />
                  </Button>
                }
              />
              <TooltipContent side="bottom">{t(($) => $.office.controls.reset_view)}</TooltipContent>
            </Tooltip>
          </div>

          {/* Fullscreen spectator toggle */}
          {onToggleFullscreen && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    onClick={onToggleFullscreen}
                    aria-label={isFullscreen
                      ? t(($) => $.office.exit_fullscreen)
                      : t(($) => $.office.fullscreen)}
                    className="size-8 text-muted-foreground hover:text-foreground"
                  >
                    {isFullscreen ? (
                      <Minimize2 className="size-3.5" />
                    ) : (
                      <Maximize2 className="size-3.5" />
                    )}
                  </Button>
                }
              />
              <TooltipContent side="bottom">
                {isFullscreen
                  ? t(($) => $.office.exit_fullscreen)
                  : t(($) => $.office.fullscreen)}
              </TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>

      {/* Main Isometric Interactive Stage */}
      <div
        ref={canvasRef}
        onMouseDown={handleMouseDown}
        onMouseMove={handleMouseMove}
        onMouseUp={handleMouseUp}
        onMouseLeave={handleMouseUp}
        className={cn(
          "relative flex-1 cursor-grab overflow-hidden bg-gradient-to-b from-muted/30 to-background active:cursor-grabbing",
        )}
      >
        {/* World Transform Container */}
        <div
          ref={worldRef}
          style={{
            transform: "translate(0px, 0px) scale(1)",
            transformOrigin: "center center",
          }}
          className="absolute inset-0 size-full transition-transform duration-100 motion-reduce:transition-none"
        >
          {/* Isometric Architectural Floor SVG */}
          <svg
            className="absolute inset-0 size-full pointer-events-none"
            viewBox="0 0 1200 750"
            preserveAspectRatio="xMidYMid meet"
          >
            <defs>
              {/* Floor Isometric Grid Pattern */}
              <pattern
                id={`${canvasId}-grid`}
                width="60"
                height="34.64"
                patternUnits="userSpaceOnUse"
              >
                <path
                  d="M 30 0 L 60 17.32 L 30 34.64 L 0 17.32 Z"
                  fill="none"
                  stroke="var(--border)"
                  strokeWidth="0.5"
                  strokeOpacity="0.25"
                />
              </pattern>

              {/* Station Gradients */}
              <linearGradient id={`${canvasId}-grad-pm`} x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="var(--info)" stopOpacity="0.16" />
                <stop offset="100%" stopColor="var(--info)" stopOpacity="0.05" />
              </linearGradient>

            </defs>

            {/* Base Grid Background */}
            <rect width="1200" height="750" fill={`url(#${canvasId}-grid)`} />

            {/* Central Leader Sanctuary Aura (Under Project Manager) */}
            <ellipse
              cx="600"
              cy="140"
              rx="180"
              ry="75"
              fill={`url(#${canvasId}-grad-pm)`}
              opacity="0.4"
            />
            <ellipse
              cx="600"
              cy="140"
              rx="130"
              ry="55"
              fill="none"
              stroke="var(--warning)"
              strokeWidth="1"
              strokeDasharray="4 4"
              opacity="0.6"
            />
          </svg>

          {/* Replay Layer (Only active during replay events, normal persistent tracks hidden) */}
          <PacketFlowLayer edges={flowEdges} activeReplay={activeReplay} />

          {/* Agent Desk HUD Overlays (Rendered at exact SVG coordinate anchors) */}
          <div className="pointer-events-none absolute inset-0 size-full">
            {desks.map((desk) => {
              // Convert SVG viewBox coordinate (1200x750) to percentage position
              const leftPercent = (desk.deskCoord.x / 1200) * 100;
              const topPercent = (desk.deskCoord.y / 750) * 100;

              return (
                <div
                  key={desk.id}
                  style={{
                    left: `${leftPercent}%`,
                    top: `${topPercent}%`,
                    transform: "translate(-50%, -50%)",
                  }}
                  className="pointer-events-auto absolute"
                >
                  <AgentDeskHud
                    desk={desk}
                    blockers={scene.blockers.filter((blocker) =>
                      (blocker.agentId && blocker.agentId === desk.agentId) ||
                      (blocker.issueId && blocker.issueId === (desk.activeTask?.issueId ?? desk.currentIssueId)),
                    )}
                    isSelected={selectedDesk?.id === desk.id}
                    onSelect={(d) => {
                      onSelectDesk?.(d);
                      onSelectStation?.(d.stationKey);
                    }}
                  />
                </div>
              );
            })}
          </div>
        </div>
      </div>

    </div>
  );
}
