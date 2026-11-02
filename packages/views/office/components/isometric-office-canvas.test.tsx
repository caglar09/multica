import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { IsometricOfficeCanvas } from "./isometric-office-canvas";
import type { OfficeStation, OfficeZoneKey, VirtualOfficeSceneData } from "../types";

vi.mock("../../i18n", () => ({ useT: () => ({ t: () => "localized" }) }));

const station = (key: OfficeZoneKey): OfficeStation => ({
  key,
  titleKey: key,
  x: 0,
  y: 0,
  width: 1,
  height: 1,
  iconName: "ShieldAlert",
  metrics: [],
});

const scene: VirtualOfficeSceneData = {
  stations: {
    pm_desk: station("pm_desk"),
    coding_pods: station("coding_pods"),
    qa_lab: station("qa_lab"),
    approval_lounge: station("approval_lounge"),
  },
  desks: [],
  flowEdges: [],
  blockers: [],
  recentEvents: [],
  speechBubbles: [],
  stats: {
    activeAgents: 0,
    runningTasks: 0,
    completedTasks: 0,
    pendingApprovals: 0,
    blocked: 0,
    failed: 0,
    waiting: 0,
    resumable: 0,
  },
  pendingApprovalsCount: 0,
};

describe("IsometricOfficeCanvas", () => {
  it("supports keyboard zoom and disables transform motion for reduced-motion users", async () => {
    const user = userEvent.setup();
    const { container } = render(<IsometricOfficeCanvas scene={scene} />);
    const zoomIn = screen.getAllByRole("button", { name: "localized" })[0]!;
    act(() => zoomIn.focus());
    await user.keyboard("{Enter}");

    const world = container.querySelector(".transition-transform");
    expect(world).toHaveStyle({ transform: "translate(0px, 0px) scale(1.15)" });
    expect(world).toHaveClass("motion-reduce:transition-none");
  });
});
