import { useState, useRef, useCallback, useEffect } from "react";
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
  selectedStationKey?: OfficeZoneKey | null;
  selectedDesk?: OfficeAgentDesk | null;
  isFullscreen?: boolean;
  onSelectStation?: (key: OfficeZoneKey) => void;
  onSelectDesk?: (desk: OfficeAgentDesk) => void;
  onPlayRecentFlow?: () => void;
  onToggleFullscreen?: () => void;
  className?: string;
}

export function IsometricOfficeCanvas({
  scene,
  activeReplay,
  selectedStationKey,
  selectedDesk,
  isFullscreen,
  onSelectStation,
  onSelectDesk,
  onPlayRecentFlow,
  onToggleFullscreen,
  className,
}: IsometricOfficeCanvasProps) {
  const { t } = useT("projects");
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [isDragging, setIsDragging] = useState(false);
  const dragStartRef = useRef({ x: 0, y: 0 });
  const canvasRef = useRef<HTMLDivElement>(null);

  // Mouse drag to pan
  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    // Only drag with primary mouse button
    if (e.button !== 0) return;
    setIsDragging(true);
    dragStartRef.current = { x: e.clientX - pan.x, y: e.clientY - pan.y };
  }, [pan]);

  const handleMouseMove = useCallback((e: React.MouseEvent) => {
    if (!isDragging) return;
    setPan({
      x: e.clientX - dragStartRef.current.x,
      y: e.clientY - dragStartRef.current.y,
    });
  }, [isDragging]);

  const handleMouseUp = useCallback(() => {
    setIsDragging(false);
  }, []);

  const handleResetView = useCallback(() => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  }, []);

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
        setZoom((prev) => {
          const next = Math.min(Math.max(prev + delta, 0.4), 2.5);
          return Number(next.toFixed(3));
        });
      } else if (e.shiftKey || Math.abs(e.deltaX) > 0) {
        // Trackpad 2-finger horizontal / vertical pan
        setPan((prev) => ({
          x: prev.x - e.deltaX,
          y: prev.y - e.deltaY,
        }));
      } else {
        // Standard mouse wheel: smooth zoom in / out
        const factor = 0.0018;
        const delta = -e.deltaY * factor;
        setZoom((prev) => {
          const next = Math.min(Math.max(prev + delta, 0.4), 2.5);
          return Number(next.toFixed(3));
        });
      }
    };

    el.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      el.removeEventListener("wheel", onWheel);
    };
  }, []);

  const { stations, desks, flowEdges, stats } = scene;

  return (
    <div
      className={cn(
        "relative flex size-full flex-col overflow-hidden bg-slate-950 font-sans text-slate-100 select-none",
        isFullscreen && "fixed inset-0 z-50",
        className,
      )}
    >
      {/* Top HUD Control Bar */}
      <div className="z-10 flex shrink-0 flex-wrap items-center justify-between gap-3 border-b border-slate-800/80 bg-slate-900/85 px-4 py-2.5 backdrop-blur-md">
        {/* Left: Quick Stats Summary */}
        <div className="flex items-center gap-2 overflow-x-auto text-xs">
          <Badge
            variant="outline"
            className="gap-1.5 border-slate-700 bg-slate-800/80 text-cyan-400 py-1"
          >
            <span className="size-1.5 rounded-full bg-cyan-400 animate-pulse" />
            <span>{t(($) => $.office.stats.active_agents)}:</span>
            <span className="font-bold">{stats.activeAgents}</span>
          </Badge>

          <Badge
            variant="outline"
            className="gap-1.5 border-slate-700 bg-slate-800/80 text-indigo-400 py-1"
          >
            <span className="size-1.5 rounded-full bg-indigo-400" />
            <span>{t(($) => $.office.stats.running_tasks)}:</span>
            <span className="font-bold">{stats.runningTasks}</span>
          </Badge>

          <Badge
            variant="outline"
            className="gap-1.5 border-slate-700 bg-slate-800/80 text-emerald-400 py-1"
          >
            <span className="size-1.5 rounded-full bg-emerald-400" />
            <span>{t(($) => $.office.stats.completed_tasks)}:</span>
            <span className="font-bold">{stats.completedTasks}</span>
          </Badge>

          {stats.pendingApprovals > 0 && (
            <Badge
              variant="outline"
              className="gap-1.5 border-amber-500/40 bg-amber-500/10 text-amber-400 py-1 animate-pulse"
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
              className="h-7 gap-1.5 bg-slate-800 text-xs font-medium text-slate-200 hover:bg-slate-700"
            >
              <Play className="size-3 text-amber-400 fill-amber-400" />
              <span>{t(($) => $.office.play_flow)}</span>
            </Button>
          )}

          {/* Zoom controls */}
          <div className="flex items-center rounded-md border border-slate-800 bg-slate-900/60 p-0.5">
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    onClick={() => setZoom((z) => Math.min(z + 0.15, 1.8))}
                    className="size-6 text-slate-400 hover:text-slate-100"
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
                    onClick={() => setZoom((z) => Math.max(z - 0.15, 0.65))}
                    className="size-6 text-slate-400 hover:text-slate-100"
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
                    className="size-6 text-slate-400 hover:text-slate-100"
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
                    className="size-7 text-slate-300 hover:bg-slate-800 hover:text-white"
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
          "relative flex-1 cursor-grab overflow-hidden active:cursor-grabbing",
          "bg-[radial-gradient(ellipse_80%_80%_at_50%_-20%,rgba(30,41,59,0.5),rgba(2,6,23,0.95))]",
        )}
      >
        {/* World Transform Container */}
        <div
          style={{
            transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
            transformOrigin: "center center",
            transition: isDragging ? "none" : "transform 0.1s ease-out",
          }}
          className="absolute inset-0 size-full min-h-[750px] min-w-[1200px]"
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
                id="office-isometric-grid"
                width="60"
                height="34.64"
                patternUnits="userSpaceOnUse"
              >
                <path
                  d="M 30 0 L 60 17.32 L 30 34.64 L 0 17.32 Z"
                  fill="none"
                  stroke="#334155"
                  strokeWidth="0.5"
                  strokeOpacity="0.25"
                />
              </pattern>

              {/* Station Gradients */}
              <linearGradient id="grad-pm" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="#0284c7" stopOpacity="0.3" />
                <stop offset="100%" stopColor="#082f49" stopOpacity="0.6" />
              </linearGradient>

              <linearGradient id="grad-coding" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="#4f46e5" stopOpacity="0.3" />
                <stop offset="100%" stopColor="#1e1b4b" stopOpacity="0.6" />
              </linearGradient>

              <linearGradient id="grad-qa" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="#9333ea" stopOpacity="0.3" />
                <stop offset="100%" stopColor="#3b0764" stopOpacity="0.6" />
              </linearGradient>

              <linearGradient id="grad-approval" x1="0%" y1="0%" x2="100%" y2="100%">
                <stop offset="0%" stopColor="#059669" stopOpacity="0.3" />
                <stop offset="100%" stopColor="#022c22" stopOpacity="0.6" />
              </linearGradient>
            </defs>

            {/* Base Grid Background */}
            <rect width="1200" height="750" fill="url(#office-isometric-grid)" />

            {/* 4 Raised Isometric Zone Podiums */}
            {/* 1. PM Desk Zone */}
            <g
              role="button"
              tabIndex={0}
              className="cursor-pointer pointer-events-auto"
              onClick={() => onSelectStation?.("pm_desk")}
              onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onSelectStation?.("pm_desk")}
            >
              <rect
                x={stations.pm_desk.x - 140}
                y={stations.pm_desk.y - 80}
                width={stations.pm_desk.width}
                height={stations.pm_desk.height}
                rx="16"
                fill="url(#grad-pm)"
                stroke={selectedStationKey === "pm_desk" ? "#38bdf8" : "#0284c7"}
                strokeWidth={selectedStationKey === "pm_desk" ? "2.5" : "1"}
                strokeOpacity="0.8"
                className="transition-all hover:stroke-cyan-400"
              />
              <text
                x={stations.pm_desk.x - 120}
                y={stations.pm_desk.y - 55}
                fill="#38bdf8"
                fontSize="12.5"
                fontWeight="700"
                letterSpacing="0.05em"
              >
                {t(($) => $.office.zones.pm_desk)}
              </text>
            </g>

            {/* 2. Coding Pods Zone */}
            <g
              role="button"
              tabIndex={0}
              className="cursor-pointer pointer-events-auto"
              onClick={() => onSelectStation?.("coding_pods")}
              onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onSelectStation?.("coding_pods")}
            >
              <rect
                x={stations.coding_pods.x - 280}
                y={stations.coding_pods.y - 140}
                width={stations.coding_pods.width}
                height={stations.coding_pods.height}
                rx="18"
                fill="url(#grad-coding)"
                stroke={selectedStationKey === "coding_pods" ? "#818cf8" : "#4f46e5"}
                strokeWidth={selectedStationKey === "coding_pods" ? "2.5" : "1"}
                strokeOpacity="0.8"
                className="transition-all hover:stroke-indigo-400"
              />
              <text
                x={stations.coding_pods.x - 255}
                y={stations.coding_pods.y - 110}
                fill="#818cf8"
                fontSize="13"
                fontWeight="700"
                letterSpacing="0.05em"
              >
                {t(($) => $.office.zones.coding_pods)}
              </text>
            </g>

            {/* 3. QA & Verification Lab Zone */}
            <g
              role="button"
              tabIndex={0}
              className="cursor-pointer pointer-events-auto"
              onClick={() => onSelectStation?.("qa_lab")}
              onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onSelectStation?.("qa_lab")}
            >
              <rect
                x={stations.qa_lab.x - 210}
                y={stations.qa_lab.y - 140}
                width={stations.qa_lab.width}
                height={stations.qa_lab.height}
                rx="18"
                fill="url(#grad-qa)"
                stroke={selectedStationKey === "qa_lab" ? "#c084fc" : "#9333ea"}
                strokeWidth={selectedStationKey === "qa_lab" ? "2.5" : "1"}
                strokeOpacity="0.8"
                className="transition-all hover:stroke-purple-400"
              />
              <text
                x={stations.qa_lab.x - 185}
                y={stations.qa_lab.y - 110}
                fill="#c084fc"
                fontSize="13"
                fontWeight="700"
                letterSpacing="0.05em"
              >
                {t(($) => $.office.zones.qa_lab)}
              </text>
            </g>

            {/* 4. Decision & Approval Lounge Zone */}
            <g
              role="button"
              tabIndex={0}
              className="cursor-pointer pointer-events-auto"
              onClick={() => onSelectStation?.("approval_lounge")}
              onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && onSelectStation?.("approval_lounge")}
            >
              <rect
                x={stations.approval_lounge.x - 140}
                y={stations.approval_lounge.y - 80}
                width={stations.approval_lounge.width}
                height={stations.approval_lounge.height}
                rx="16"
                fill="url(#grad-approval)"
                stroke={selectedStationKey === "approval_lounge" ? "#34d399" : "#059669"}
                strokeWidth={selectedStationKey === "approval_lounge" ? "2.5" : "1"}
                strokeOpacity="0.8"
                className="transition-all hover:stroke-emerald-400"
              />
              <text
                x={stations.approval_lounge.x - 120}
                y={stations.approval_lounge.y - 55}
                fill="#34d399"
                fontSize="12.5"
                fontWeight="700"
                letterSpacing="0.05em"
              >
                {t(($) => $.office.zones.approval_lounge)}
              </text>
            </g>
          </svg>

          {/* Animated Neon Packet Flow Layer */}
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
