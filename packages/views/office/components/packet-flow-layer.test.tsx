import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PacketFlowLayer } from "./packet-flow-layer";
import type { OfficeFlowEdge, OfficeReplayEvent } from "../types";

vi.mock("../../i18n", () => ({ useT: () => ({ t: () => "localized" }) }));

const edge: OfficeFlowEdge = {
  id: "handoff-1",
  kind: "review_handoff",
  fromStation: "coding_pods",
  toStation: "qa_lab",
  fromCoord: { x: 100, y: 200 },
  toCoord: { x: 700, y: 400 },
  issueId: "issue-1",
  relatedCount: 1,
  active: true,
};

const routedEvent: OfficeReplayEvent = {
  id: "event-1",
  timestamp: "2026-10-31T10:00:00.000Z",
  type: "workflow.review_requested",
  fromStation: "coding_pods",
  toStation: "qa_lab",
  issueId: "issue-1",
};

describe("PacketFlowLayer", () => {
  it("draws a semantic handoff route and omits motion for events without a route", () => {
    const { container, rerender } = render(
      <PacketFlowLayer edges={[edge]} activeReplay={routedEvent} />,
    );
    expect(screen.getByRole("img", { name: "localized" })).toBeInTheDocument();
    expect(container.querySelector("path")?.getAttribute("stroke-dasharray")).toBe("7 4");
    expect(container.querySelectorAll("animateMotion")).toHaveLength(2);
    expect(container.querySelector("circle")?.getAttribute("class")).toContain("motion-reduce:hidden");

    rerender(<PacketFlowLayer edges={[edge]} activeReplay={{ ...routedEvent, fromStation: undefined, toStation: undefined }} />);
    expect(container.querySelectorAll("animateMotion")).toHaveLength(0);
  });
});
