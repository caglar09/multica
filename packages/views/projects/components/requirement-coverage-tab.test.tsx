// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { RequirementCoverageTab } from "./requirement-coverage-tab";
import type { AutonomousProjectSnapshot } from "@multica/core/types";

vi.mock("../../i18n", () => {
  const resources = {
    autonomous_evidence: {
      requirement_coverage: "Requirement coverage",
      coverage_description: "Coverage description",
      plan_nodes: "Plan nodes",
      no_linked_plan_node: "No linked plan node",
      verification_gates: "Verification gates",
      no_verification_evidence: "No verification evidence",
      latest_deployments: "Latest deployments",
      no_deployment_recorded: "No deployment recorded",
      stable_requirements_missing: "Stable requirement IDs are not available.",
      non_goals: "Non-goals",
      non_goals_description: "Excluded from the specification.",
      non_goal: "Non-goal",
      epic: "Epic",
      task: "Task",
      coverage_status: {
        unmapped: "Unmapped",
        verification_failed: "Verification failed",
        verified: "Verified",
        awaiting_verification: "Awaiting verification",
        in_progress: "In progress",
      },
    },
  };
  return {
    useT: () => ({
      t: (selector: (value: typeof resources) => string) => selector(resources),
    }),
  };
});

describe("RequirementCoverageTab", () => {
  it("links a stable requirement to its task, verification gate, and deployed commit", () => {
    const snapshot = {
      plan: {
        id: "plan-id",
        revision: 2,
        goal: "Ship the project cockpit",
        status: "active",
        plan_digest: "plan-digest",
        approved_at: null,
        approved_by: null,
        planner_name: "planner",
        planner_model: null,
        specification: {
          requirement_details: [{
            id: "REQ-12",
            text: "Show task verification status",
            rationale: "Make completion evidence visible",
            source: "customer",
          }],
          non_goals: ["Automatic production release"],
        },
        policy: {},
        nodes: [{
          id: "node-id",
          key: "coverage-ui",
          is_epic: false,
          parent_epic_key: "cockpit",
          kind: "implementation",
          title: "Requirement coverage tab",
          status: "completed",
          priority: 1,
          risk: "low",
          required_role_family: null,
          assigned_role: null,
          assigned_agent_id: null,
          materialized_issue_id: null,
          attempt: 1,
          max_attempts: 2,
          blocked_category: null,
          blocked_reason: null,
          issue_status: null,
          workflow_state: null,
          acceptance_criteria: [],
          requirement_ids: ["REQ-12"],
          updated_at: "2026-10-28T00:00:00Z",
        }],
        edges: [],
        updated_at: "2026-10-28T00:00:00Z",
      },
      quality_gates: [{
        id: "gate-id",
        node_id: "node-id",
        gate_type: "unit_test",
        status: "passed",
        required: true,
        evidence: {},
        last_error: null,
        updated_at: "2026-10-28T00:00:00Z",
      }],
      deployments: [{
        id: "deployment-id",
        environment: "production",
        status: "succeeded",
        commit_sha: "abc123",
        manifest_digest: "manifest-digest",
        updated_at: "2026-10-28T00:00:00Z",
      }],
    } satisfies Pick<AutonomousProjectSnapshot, "plan" | "quality_gates" | "deployments">;

    render(<RequirementCoverageTab snapshot={snapshot} />);

    expect(screen.getByText("REQ-12")).toBeTruthy();
    expect(screen.getByText(/Requirement coverage tab/)).toBeTruthy();
    expect(screen.getByText("unit_test · passed")).toBeTruthy();
    expect(screen.getByText("abc123")).toBeTruthy();
    expect(screen.getByText("Automatic production release")).toBeTruthy();
  });
});
