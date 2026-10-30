"use client";

import { useId } from "react";
import type { OfficeFlowEdge, OfficeReplayEvent } from "../types";

interface PacketFlowLayerProps {
  edges: OfficeFlowEdge[];
  activeReplay?: OfficeReplayEvent | null;
  className?: string;
}

export function PacketFlowLayer({
  edges,
  activeReplay,
  className,
}: PacketFlowLayerProps) {
  const layerId = useId();

  return (
    <svg
      className={`pointer-events-none absolute inset-0 size-full overflow-visible ${className || ""}`}
      viewBox="0 0 1200 750"
      preserveAspectRatio="xMidYMid meet"
    >
      <defs>
        {/* Glow Filters */}
        <filter id={`${layerId}-glow-cyan`} x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="3" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
        <filter id={`${layerId}-glow-purple`} x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="3" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
        <filter id={`${layerId}-glow-amber`} x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="4" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
        <filter id={`${layerId}-glow-emerald`} x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="3" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>

      {/* Render Edges */}
      {edges.map((edge) => {
        const { fromCoord, toCoord, color, active, id } = edge;
        // Compute isometric curved control point
        const midX = (fromCoord.x + toCoord.x) / 2;
        const midY = (fromCoord.y + toCoord.y) / 2 - 25;
        const pathData = `M ${fromCoord.x} ${fromCoord.y} Q ${midX} ${midY} ${toCoord.x} ${toCoord.y}`;
        const pathId = `${layerId}-path-${id}`;

        const isReplayingThisEdge =
          activeReplay &&
          activeReplay.fromStation === edge.fromStation &&
          activeReplay.toStation === edge.toStation;

        return (
          <g key={id} className="transition-opacity duration-300">
            {/* Base Highway Track */}
            <path
              d={pathData}
              fill="none"
              stroke={color}
              strokeWidth={isReplayingThisEdge ? "3" : active ? "1.5" : "0.75"}
              strokeOpacity={isReplayingThisEdge ? "0.9" : active ? "0.45" : "0.15"}
              strokeDasharray={active ? "6 4" : "3 3"}
              className={active ? "animate-[dash_20s_linear_infinite]" : undefined}
            />

            {/* Hidden Track for animateMotion */}
            <path id={pathId} d={pathData} fill="none" stroke="none" />

            {/* Continuous Pulse Packet on Active Tracks */}
            {active && (
              <circle r="3.5" fill={color} filter={`url(#${layerId}-glow-${getColorName(color)})`}>
                <animateMotion
                  dur={edge.pulseSpeed === "fast" ? "1.8s" : "3.2s"}
                  repeatCount="indefinite"
                  rotate="auto"
                >
                  <mpath href={`#${pathId}`} />
                </animateMotion>
              </circle>
            )}

            {/* Active Replay Highlight Packet */}
            {isReplayingThisEdge && (
              <g>
                <circle
                  r="6"
                  fill="#ffffff"
                  stroke={color}
                  strokeWidth="2.5"
                  filter={`url(#${layerId}-glow-amber)`}
                >
                  <animateMotion dur="2.2s" repeatCount="indefinite" rotate="auto">
                    <mpath href={`#${pathId}`} />
                  </animateMotion>
                </circle>

                {/* Second lagging trail packet */}
                <circle r="3.5" fill={color} opacity="0.75">
                  <animateMotion
                    dur="2.2s"
                    begin="0.15s"
                    repeatCount="indefinite"
                    rotate="auto"
                  >
                    <mpath href={`#${pathId}`} />
                  </animateMotion>
                </circle>
              </g>
            )}
          </g>
        );
      })}

      {/* Replay Floating Banner above the scene */}
      {activeReplay && (
        <g className="animate-in fade-in slide-in-from-top-2 duration-300">
          <rect
            x="360"
            y="42"
            width="280"
            height="32"
            rx="16"
            fill="#090d16"
            stroke="#f59e0b"
            strokeWidth="1.5"
            filter="drop-shadow(0 4px 12px rgba(245, 158, 11, 0.25))"
          />
          <circle cx="376" cy="58" r="4.5" fill="#f59e0b" className="animate-pulse" />
          <text
            x="392"
            y="63"
            fill="#f3f4f6"
            fontSize="11.5"
            fontWeight="600"
            fontFamily="inherit"
          >
            {activeReplay.title.length > 34
              ? `${activeReplay.title.slice(0, 32)}…`
              : activeReplay.title}
          </text>
        </g>
      )}
    </svg>
  );
}

function getColorName(hex: string): string {
  if (hex.includes("06b6d4")) return "cyan";
  if (hex.includes("a855f7")) return "purple";
  if (hex.includes("10b981")) return "emerald";
  if (hex.includes("f59e0b")) return "amber";
  return "cyan";
}
