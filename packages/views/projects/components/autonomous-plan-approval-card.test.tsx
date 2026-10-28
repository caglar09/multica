import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AutonomousProjectPlanSnapshot } from "@multica/core/types";
import { AutonomousPlanApprovalCard } from "./autonomous-plan-approval-card";

const plan: AutonomousProjectPlanSnapshot = {
  id: "plan-1",
  revision: 3,
  goal: "Ship the feature",
  status: "awaiting_approval",
  plan_digest: "sha256:9b2f9f2f",
  approved_at: null,
  approved_by: null,
  planner_name: "planner",
  planner_model: null,
  specification: {},
  policy: {},
  nodes: [],
  edges: [],
  updated_at: "2026-10-27T00:00:00Z",
};

const labels = {
  title: "Plan approval",
  pendingDescription: "Review this exact plan revision before work starts.",
  approvedDescription: "This plan revision has an approval record.",
  digest: "Plan digest",
  digestMissing: "This plan has no digest and cannot be approved.",
  adminOnly: "Only a workspace admin can approve this plan.",
  approve: "Approve this revision",
};

describe("AutonomousPlanApprovalCard", () => {
  it("shows the exact digest and sends approval only from an authorized control", () => {
    const onApprove = vi.fn();
    render(
      <AutonomousPlanApprovalCard
        plan={plan}
        canControl
        isPending={false}
        labels={labels}
        onApprove={onApprove}
      />,
    );

    expect(screen.getByText(plan.plan_digest)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: labels.approve }));
    expect(onApprove).toHaveBeenCalledOnce();
  });

  it("does not offer the action to non-admin users", () => {
    render(
      <AutonomousPlanApprovalCard
        plan={plan}
        canControl={false}
        isPending={false}
        labels={labels}
        onApprove={vi.fn()}
      />,
    );

    expect(screen.getByText(labels.adminOnly)).toBeVisible();
    expect(screen.queryByRole("button", { name: labels.approve })).toBeNull();
  });
});
