// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AutonomousProjectSpecificationApproval } from "@multica/core/types";
import {
  AutonomousSpecificationApprovalCard,
  type AutonomousSpecificationApprovalLabels,
} from "./autonomous-specification-approval-card";

afterEach(cleanup);

const digest = "a".repeat(64);
const specification: AutonomousProjectSpecificationApproval = {
  revision_id: "spec-1",
  revision: 2,
  digest,
  source_kind: "change_request",
  status: "awaiting_approval",
  is_proposal: true,
  change_request_id: "change-1",
  specification: {
    goal: "Deliver the approved project scope",
    summary: "A reviewed capability",
    requirements: [{
      id: "REQ-1",
      text: "Keep imported work unassigned",
      rationale: "Local adoption is required before dispatch.",
      source: "project brief",
    }],
    non_functional_requirements: [],
    constraints: [],
    non_goals: [],
    definition_of_done: [],
    acceptance_expectations: [],
  },
  approval_history: [{
    id: "event-1",
    revision: 1,
    digest: "b".repeat(64),
    decision: "rejected",
    actor_type: "human",
    actor_display: "Workspace admin",
    rationale: "Needs narrower scope.",
    origin: "local",
    acted_at: "2026-11-02T12:00:00Z",
  }],
};

const labels: AutonomousSpecificationApprovalLabels = {
  title: "Specification approval",
  summary: "Review the proposed project requirements.",
  requirements: "Requirements",
  requirementsEmpty: "No requirements in this specification.",
  digest: "Specification digest",
  approve: "Approve specification",
  reject: "Reject specification",
  adminOnly: "Only a workspace admin can decide.",
  localAdoptionRequired: "Adopt this specification locally before dispatch.",
  awaitingApproval: "Awaiting approval",
  approved: "Approved",
  rejected: "Rejected",
  approvalHistory: "Approval history",
  historyEmpty: "No approval events recorded.",
  eventApproved: "Approved",
  eventRejected: "Rejected",
  eventUnknown: "Unknown decision",
  originLocal: "Local",
  originImported: "Imported",
  originUnknown: "Unknown origin",
  rejectionTitle: "Reject specification",
  rejectionDescription: "Give a reason for the project manager.",
  rejectionReason: "Reason",
  rejectionReasonPlaceholder: "Explain what needs to change",
  rejectionReasonRequired: "A reason is required.",
  rejectionFailed: "The specification could not be rejected.",
  cancel: "Cancel",
  submitRejection: "Confirm rejection",
};

describe("AutonomousSpecificationApprovalCard", () => {
  it("shows requirement coverage, exact digest, approval history, and requires a rejection reason", async () => {
    const onReject = vi.fn(async () => true);
    render(
      <AutonomousSpecificationApprovalCard
        specification={specification}
        canControl
        isPending={false}
        labels={labels}
        onApprove={vi.fn()}
        onReject={onReject}
      />,
    );

    expect(screen.getByText("REQ-1")).toBeTruthy();
    expect(screen.getByText("Keep imported work unassigned")).toBeTruthy();
    expect(screen.getByText(digest)).toBeTruthy();
    expect(screen.getByText("Needs narrower scope.")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: labels.reject }));
    fireEvent.click(screen.getByRole("button", { name: labels.submitRejection }));
    expect(await screen.findByText(labels.rejectionReasonRequired)).toBeTruthy();

    fireEvent.change(screen.getByLabelText(labels.rejectionReason), {
      target: { value: "  Narrow the project scope.  " },
    });
    fireEvent.click(screen.getByRole("button", { name: labels.submitRejection }));
    await waitFor(() => expect(onReject).toHaveBeenCalledWith("Narrow the project scope."));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("does not offer approval actions for imported specifications before local adoption", () => {
    render(
      <AutonomousSpecificationApprovalCard
        specification={{ ...specification, is_proposal: false, change_request_id: null }}
        canControl
        isPending={false}
        labels={labels}
        onApprove={vi.fn()}
        onReject={vi.fn(async () => true)}
      />,
    );

    expect(screen.getByText(labels.localAdoptionRequired)).toBeTruthy();
    expect(screen.queryByRole("button", { name: labels.approve })).toBeNull();
    expect(screen.queryByRole("button", { name: labels.reject })).toBeNull();
  });
});
