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

      {/* Render Edges only when actively replaying a specific event (no intrusive persistent lines between desks) */}
      {edges.map((edge) => {
        const { fromCoord, toCoord, color, id } = edge;
        const isReplayingThisEdge =
          activeReplay &&
          activeReplay.fromStation === edge.fromStation &&
          activeReplay.toStation === edge.toStation;

        if (!isReplayingThisEdge) return null;

        // Compute isometric curved control point
        const midX = (fromCoord.x + toCoord.x) / 2;
        const midY = (fromCoord.y + toCoord.y) / 2 - 25;
        const pathData = `M ${fromCoord.x} ${fromCoord.y} Q ${midX} ${midY} ${toCoord.x} ${toCoord.y}`;
        const pathId = `${layerId}-path-${id}`;

        return (
          <g key={id} className="transition-opacity duration-300">
            {/* Temporary Replay Track */}
            <path
              d={pathData}
              fill="none"
              stroke={color}
              strokeWidth="2.5"
              strokeOpacity="0.8"
              strokeDasharray="6 4"
              className="animate-[dash_20s_linear_infinite]"
            />

            {/* Hidden Track for animateMotion */}
            <path id={pathId} d={pathData} fill="none" stroke="none" />

            {/* Active Replay Highlight Packet */}
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
