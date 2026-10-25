import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { DeploymentDetailCard } from "./components/deployment-detail-card";
import type {
  DeploymentOperation,
  DeploymentTarget,
} from "@multica/core/types";

describe("DeploymentDetailCard", () => {
  const mockTarget: DeploymentTarget = {
    id: "tgt_1",
    workspace_id: "ws_1",
    connection_id: "conn_vercel",
    name: "Production Web",
    environment: "production",
    config: {
      project_name: "my-web-app",
      team_slug: "acme-corp",
      repository: "https://github.com/caglar09/clock-time",
      framework: "nextjs",
    },
    created_at: "2026-09-13T00:00:00Z",
    updated_at: "2026-09-13T00:00:00Z",
  };

  const mockReadyOperation: DeploymentOperation = {
    id: "op_1",
    workspace_id: "ws_1",
    target_id: "tgt_1",
    action: "deploy",
    idempotency_key: "idemp_1",
    status: "ready",
    attempt_count: 1,
    max_attempts: 3,
    provider_operation_id: "dpl_ready123",
    request: {
      git_ref: "refs/heads/main",
      commit_sha: "c82e4ff816e642c2907b6953e19cb9",
      commit_message: "feat: add vercel integration",
      environment: "production",
      target_name: "Production Web",
    },
    evidence: {
      url: "https://my-web-app-prod.vercel.app",
      inspector_url:
        "https://vercel.com/acme-corp/my-web-app/deployments/dpl_ready123",
      deployment_id: "dpl_ready123",
    },
    created_at: "2026-09-13T10:00:00Z",
    updated_at: "2026-09-13T10:05:00Z",
  };

  it("renders status badge, environment, and commit details", () => {
    renderWithI18n(
      <DeploymentDetailCard
        operation={mockReadyOperation}
        target={mockTarget}
      />,
    );

    expect(screen.getByTestId("status-badge-succeeded")).toBeInTheDocument();
    expect(
      screen.getByTestId("environment-badge-production"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("deployment-commit-sha")).toHaveTextContent(
      "c82e4ff",
    );
    expect(screen.getByTestId("deployment-git-ref")).toHaveTextContent(
      "refs/heads/main",
    );
    expect(screen.getByTestId("deployment-commit-message")).toHaveTextContent(
      "feat: add vercel integration",
    );
  });

  it("links to external Vercel deployment URL and Vercel dashboard", () => {
    renderWithI18n(
      <DeploymentDetailCard
        operation={mockReadyOperation}
        target={mockTarget}
      />,
    );

    // 1. External Vercel Deployment URL
    const deploymentLink = screen.getByTestId("external-deployment-url");
    expect(deploymentLink).toBeInTheDocument();
    expect(deploymentLink).toHaveAttribute(
      "href",
      "https://my-web-app-prod.vercel.app",
    );
    expect(deploymentLink).toHaveAttribute("target", "_blank");
    expect(deploymentLink).toHaveAttribute("rel", "noopener noreferrer");

    // 2. External Vercel Dashboard / Inspector URL
    const dashboardLink = screen.getByTestId("external-dashboard-url");
    expect(dashboardLink).toBeInTheDocument();
    expect(dashboardLink).toHaveAttribute(
      "href",
      "https://vercel.com/acme-corp/my-web-app",
    );
    expect(dashboardLink).toHaveAttribute("target", "_blank");
    expect(dashboardLink).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("normalizes provider deployment hostnames to HTTPS URLs", () => {
    renderWithI18n(
      <DeploymentDetailCard
        operation={{
          ...mockReadyOperation,
          evidence: { deployment_url: "my-web-app.vercel.app" },
        }}
        target={mockTarget}
      />,
    );

    expect(screen.getByTestId("external-deployment-url")).toHaveAttribute(
      "href",
      "https://my-web-app.vercel.app",
    );
  });

  it("opens provider logs when supported", async () => {
    const user = userEvent.setup();
    const onViewLogs = vi.fn();
    renderWithI18n(
      <DeploymentDetailCard
        operation={mockReadyOperation}
        target={mockTarget}
        onViewLogs={onViewLogs}
      />,
    );

    await user.click(screen.getByTestId("deployment-logs-button"));
    expect(onViewLogs).toHaveBeenCalledWith(mockReadyOperation);
  });

  it("builds the source revision link from the target repository", () => {
    renderWithI18n(
      <DeploymentDetailCard
        operation={mockReadyOperation}
        target={mockTarget}
      />,
    );

    expect(screen.getByTestId("deployment-source-revision-link")).toHaveAttribute(
      "href",
      "https://github.com/caglar09/clock-time/commit/c82e4ff816e642c2907b6953e19cb9",
    );
  });

  it("handles fallback dashboard URL constructed from target when inspector_url is omitted", () => {
    const operationWithoutInspector: DeploymentOperation = {
      ...mockReadyOperation,
      evidence: {
        url: "https://preview.vercel.app",
      },
    };

    renderWithI18n(
      <DeploymentDetailCard
        operation={operationWithoutInspector}
        target={mockTarget}
      />,
    );

    const dashboardLink = screen.getByTestId("external-dashboard-url");
    expect(dashboardLink).toHaveAttribute(
      "href",
      "https://vercel.com/acme-corp/my-web-app",
    );
  });

  it("normalizes Vercel production inspector URLs to the project dashboard", () => {
    renderWithI18n(
      <DeploymentDetailCard
        operation={{
          ...mockReadyOperation,
          evidence: {
            inspector_url:
              "https://vercel.com/caglar-yildirims-projects/my-clock%20(production)/dpl_ready123",
          },
        }}
      />,
    );

    expect(screen.getByTestId("external-dashboard-url")).toHaveAttribute(
      "href",
      "https://vercel.com/caglar-yildirims-projects/my-clock",
    );
  });

  it("supports triggering redeploy when action handler is provided", async () => {
    const user = userEvent.setup();
    const handleRedeploy = vi.fn();

    renderWithI18n(
      <DeploymentDetailCard
        operation={mockReadyOperation}
        target={mockTarget}
        onTriggerRedeploy={handleRedeploy}
      />,
    );

    const redeployBtn = screen.getByTestId("trigger-redeploy-button");
    expect(redeployBtn).toBeInTheDocument();

    await user.click(redeployBtn);
    expect(handleRedeploy).toHaveBeenCalledWith(mockTarget);
  });
});
