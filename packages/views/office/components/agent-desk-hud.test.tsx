import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AgentDeskHud } from "./agent-desk-hud";
import type { OfficeAgentDesk, OfficeBlocker } from "../types";

vi.mock("../../i18n", () => ({ useT: () => ({ t: () => "localized" }) }));

const desk: OfficeAgentDesk = {
  id: "desk-1",
  agentId: "agent-1",
  agentName: "Reviewer",
  role: "Reviewer",
  status: "failed",
  queuedTasks: [],
  completedTasksCount: 0,
  totalAssignedTasksCount: 1,
  stationKey: "qa_lab",
  deskCoord: { x: 600, y: 400 },
};

const blocker: OfficeBlocker = {
  id: "blocker-1",
  kind: "failed_run",
  severity: "error",
  agentId: "agent-1",
  reasonKey: "projects.office.blockers.reasons.failed_run",
  action: { kind: "retry_run", targetId: "run-private-id" },
};

describe("AgentDeskHud", () => {
  it("shows a localized failure and selects the desk with the keyboard", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<AgentDeskHud desk={desk} blockers={[blocker]} onSelect={onSelect} />);

    const button = screen.getByRole("button", { name: "localized" });
    expect(button).toHaveAttribute("aria-pressed", "false");
    button.focus();
    await user.keyboard("{Enter}");

    expect(onSelect).toHaveBeenCalledWith(desk);
    expect(screen.getAllByText("localized").length).toBeGreaterThan(1);
  });
});
