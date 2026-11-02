import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { OfficeInspectorDrawer } from "./office-inspector-drawer";
import type { OfficeBlocker, OfficeReplayEvent, VirtualOfficeSceneData } from "../types";

vi.mock("../../i18n", () => ({
  useT: () => ({ t: () => "localized" }),
  useLocale: () => "en",
}));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/ws/issues/${id}` }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children, ...props }: React.ComponentProps<"a">) => (
    <a href={href} {...props}>{children}</a>
  ),
}));

const blocker: OfficeBlocker = {
  id: "blocker-1",
  kind: "issue_dependency",
  severity: "waiting",
  issueId: "issue-1",
  dependencyIssueIds: ["private-dependency-id"],
  reasonKey: "projects.office.blockers.reasons.issue_dependency",
  params: { dependencyCount: 1 },
  action: { kind: "open_issue", targetId: "private-issue-id" },
};

const event: OfficeReplayEvent = {
  id: "event-1",
  timestamp: "2026-10-31T10:00:00.000Z",
  type: "workflow.review_requested",
  fromStation: "coding_pods",
  toStation: "qa_lab",
  issueId: "issue-1",
};

const scene: VirtualOfficeSceneData = {
  stations: {} as VirtualOfficeSceneData["stations"],
  desks: [],
  flowEdges: [],
  blockers: [blocker],
  recentEvents: [event],
  speechBubbles: [],
  stats: {
    activeAgents: 0,
    runningTasks: 0,
    completedTasks: 0,
    pendingApprovals: 0,
    blocked: 1,
    failed: 0,
    waiting: 1,
    resumable: 0,
  },
  pendingApprovalsCount: 0,
};

describe("OfficeInspectorDrawer", () => {
  it("shows blocker actions without exposing IDs and replays only routed events", async () => {
    const user = userEvent.setup();
    const onBlockerAction = vi.fn();
    const onReplayEvent = vi.fn();
    render(
      <OfficeInspectorDrawer
        scene={scene}
        selectedStationKey="approval_lounge"
        activeReplay={event}
        isOpen
        onClose={vi.fn()}
        onBlockerAction={onBlockerAction}
        onReplayEvent={onReplayEvent}
      />,
    );

    const issueLink = document.querySelector('a[href="/ws/issues/private-issue-id"]');
    expect(issueLink).toBeInTheDocument();
    expect(onBlockerAction).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toContain("private-issue-id");
    expect(document.body.textContent).not.toContain("private-dependency-id");

    await user.click(screen.getAllByRole("button", { name: "localized" })[0]!);
    const feedButtons = screen.getAllByRole("button", { name: "localized" });
    await user.click(feedButtons[feedButtons.length - 1]!);
    expect(onReplayEvent).toHaveBeenCalledWith(event);
  });
});
