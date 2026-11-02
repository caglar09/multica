"use client";

import { memo, useId } from "react";
import { useT } from "../../i18n";
import type { OfficeFlowEdge, OfficeFlowEdgeKind, OfficeReplayEvent } from "../types";

interface PacketFlowLayerProps {
  edges: OfficeFlowEdge[];
  activeReplay?: OfficeReplayEvent | null;
  className?: string;
}

const EDGE_PRESENTATION: Record<OfficeFlowEdgeKind, { color: string; dash: string }> = {
  task_dispatch: { color: "var(--info)", dash: "none" },
  review_handoff: { color: "var(--primary)", dash: "7 4" },
  plan_dependency: { color: "var(--warning)", dash: "2 4" },
  issue_dependency: { color: "var(--destructive)", dash: "7 4" },
  escalation: { color: "var(--destructive)", dash: "2 3" },
};

export const PacketFlowLayer = memo(function PacketFlowLayer({
  edges,
  activeReplay,
  className,
}: PacketFlowLayerProps) {
  const layerId = useId();
  const { t } = useT("projects");
  const activityLabel = (() => {
    if (!activeReplay) return "";
    switch (activeReplay.type) {
      case "task.dispatched": return t(($) => $.office.activities.task_dispatched);
      case "task.completed": return t(($) => $.office.activities.task_completed);
      case "task.failed": return t(($) => $.office.activities.task_failed);
      case "workflow.review_requested": return t(($) => $.office.activities.review_requested);
      case "workflow.reviewed": return t(($) => $.office.activities.reviewed);
      case "plan.approved": return t(($) => $.office.activities.plan_approved);
      case "escalation.created": return t(($) => $.office.activities.escalation_created);
    }
  })();
  const routedEdges = activeReplay?.fromStation && activeReplay.toStation
    ? edges.filter((edge) =>
      edge.fromStation === activeReplay.fromStation &&
      edge.toStation === activeReplay.toStation &&
      (!activeReplay.issueId || !edge.issueId || edge.issueId === activeReplay.issueId),
    )
    : [];

  return (
    <svg
      aria-label={activityLabel || undefined}
      className={`pointer-events-none absolute inset-0 size-full overflow-visible ${className || ""}`}
      role={activeReplay ? "img" : undefined}
      viewBox="0 0 1200 750"
      preserveAspectRatio="xMidYMid meet"
    >
      {activeReplay && <title>{activityLabel}</title>}

      <defs>
        <filter id={`${layerId}-glow`} x="-30%" y="-30%" width="160%" height="160%">
          <feGaussianBlur stdDeviation="3" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>

      {routedEdges.map((edge) => {
        const style = EDGE_PRESENTATION[edge.kind];
        const midX = (edge.fromCoord.x + edge.toCoord.x) / 2;
        const midY = (edge.fromCoord.y + edge.toCoord.y) / 2 - 25;
        const pathData = `M ${edge.fromCoord.x} ${edge.fromCoord.y} Q ${midX} ${midY} ${edge.toCoord.x} ${edge.toCoord.y}`;
        const pathId = `${layerId}-path-${edge.id}`;

        return (
          <g key={edge.id}>
            <title>{t(($) => $.office.flow_kinds[edge.kind])}</title>
            <path
              d={pathData}
              fill="none"
              stroke={style.color}
              strokeWidth="2.5"
              strokeOpacity="0.8"
              strokeDasharray={style.dash}
            />
            <path id={pathId} d={pathData} fill="none" stroke="none" />
            <circle
              r="6"
              fill="var(--background)"
              stroke={style.color}
              strokeWidth="2.5"
              filter={`url(#${layerId}-glow)`}
              className="motion-reduce:hidden"
            >
              <animateMotion dur="2.2s" repeatCount="indefinite" rotate="auto">
                <mpath href={`#${pathId}`} />
              </animateMotion>
            </circle>
            <circle r="3.5" fill={style.color} opacity="0.75" className="motion-reduce:hidden">
              <animateMotion dur="2.2s" begin="0.15s" repeatCount="indefinite" rotate="auto">
                <mpath href={`#${pathId}`} />
              </animateMotion>
            </circle>
          </g>
        );
      })}

      {activeReplay && (
        <g>
          <rect
            x="360"
            y="42"
            width="480"
            height="36"
            rx="18"
            fill="var(--popover)"
            stroke="var(--warning)"
            strokeWidth="1.5"
          />
          <circle cx="379" cy="60" r="4.5" fill="var(--warning)" />
          <text
            x="395"
            y="64"
            fill="var(--popover-foreground)"
            fontSize="var(--text-caption)"
            fontWeight="600"
            fontFamily="inherit"
          >
            {activityLabel.length > 56 ? `${activityLabel.slice(0, 54)}…` : activityLabel}
          </text>
        </g>
      )}
    </svg>
  );
});
