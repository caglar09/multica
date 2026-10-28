// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { afterEach, describe, it, expect, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { DeploymentDetailCard } from "./components/deployment-detail-card";
import type {
  DeploymentOperation,
  DeploymentTarget,
  CapabilitySet,
} from "@multica/core/types";

afterEach(cleanup);

describe("DeploymentDetailCard - Capability-Gated Actions", () => {
  const mockPreviewTarget: DeploymentTarget = {
    id: "tgt_preview",
    workspace_id: "ws_1",
    connection_id: "conn_1",
    name: "Preview Target",
    environment: "preview",
    config: {
      project_name: "preview-target",
      repository: "https://github.com/caglar09/multica",
    },
    capabilities: {
      promote: true,
      rollback: false,
      cancel: true,
      logs: true,
    },
    created_at: "2026-09-13T00:00:00Z",
    updated_at: "2026-09-13T00:00:00Z",
  };

  const mockProductionTarget: DeploymentTarget = {
    id: "tgt_prod",
    workspace_id: "ws_1",
    connection_id: "conn_1",
    name: "Production Target",
    environment: "production",
    config: {
      project_name: "production-target",
    },
    capabilities: {
      promote: false,
      rollback: true,
      cancel: true,
      logs: true,
    },
    created_at: "2026-09-13T00:00:00Z",
    updated_at: "2026-09-13T00:00:00Z",
  };

  const mockSucceededPreviewOp: DeploymentOperation = {
    id: "op_prev_success",
    workspace_id: "ws_1",
    target_id: "tgt_preview",
    action: "deploy",
    idempotency_key: "idemp_1",
    status: "ready",
    attempt_count: 1,
    max_attempts: 3,
    request: {
      git_ref: "refs/heads/feature-1",
      commit_sha: "abcdef1234567890",
      commit_message: "feat: capability testing",
      environment: "preview",
      target_name: "Preview Target",
    },
    evidence: {
      url: "https://preview.multica.app",
      deployment_id: "dpl_prev1",
    },
    created_at: "2026-09-13T10:00:00Z",
    updated_at: "2026-09-13T10:05:00Z",
  };

  const mockSucceededProdOp: DeploymentOperation = {
    id: "op_prod_success",
    workspace_id: "ws_1",
    target_id: "tgt_prod",
    action: "deploy",
    idempotency_key: "idemp_2",
    status: "ready",
    attempt_count: 1,
    max_attempts: 3,
    request: {
      git_ref: "refs/heads/main",
      commit_sha: "1234567890abcdef",
      commit_message: "feat: prod release",
      environment: "production",
      target_name: "Production Target",
    },
    evidence: {
      url: "https://multica.app",
      deployment_id: "dpl_prod1",
    },
    created_at: "2026-09-13T10:00:00Z",
    updated_at: "2026-09-13T10:05:00Z",
  };

  const mockRunningOp: DeploymentOperation = {
    id: "op_running",
    workspace_id: "ws_1",
    target_id: "tgt_preview",
    action: "deploy",
    idempotency_key: "idemp_3",
    status: "running",
    attempt_count: 1,
    max_attempts: 3,
    request: {
      git_ref: "refs/heads/feature-1",
      commit_sha: "abcdef1234567890",
      commit_message: "feat: capability testing",
      environment: "preview",
      target_name: "Preview Target",
    },
    evidence: {
      url: "https://preview.multica.app",
    },
    created_at: "2026-09-13T10:00:00Z",
    updated_at: "2026-09-13T10:01:00Z",
  };

  describe("Promote action capability gating", () => {
    it("renders promote button when target has promote capability and operation is succeeded", async () => {
      const user = userEvent.setup();
      const onPromote = vi.fn();

      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededPreviewOp}
          target={mockPreviewTarget}
          onPromote={onPromote}
        />,
      );

      const promoteBtn = screen.getByTestId("deployment-promote-button");
      expect(promoteBtn).toBeInTheDocument();

      await user.click(promoteBtn);
      expect(onPromote).toHaveBeenCalledWith(
        mockSucceededPreviewOp,
        mockPreviewTarget,
      );
    });

    it("hides promote button when target capabilities explicitly disable promote", () => {
      const disabledPromoteTarget: DeploymentTarget = {
        ...mockPreviewTarget,
        capabilities: {
          ...mockPreviewTarget.capabilities!,
          promote: false,
        },
      };

      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededPreviewOp}
          target={disabledPromoteTarget}
          onPromote={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("deployment-promote-button"),
      ).not.toBeInTheDocument();
    });

    it("hides promote button on production environments even if promote capability is true", () => {
      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededProdOp}
          target={{
            ...mockProductionTarget,
            capabilities: {
              ...mockProductionTarget.capabilities!,
              promote: true,
            },
          }}
          onPromote={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("deployment-promote-button"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Rollback action capability gating", () => {
    it("renders rollback button on production target when rollback capability is enabled", async () => {
      const user = userEvent.setup();
      const onRollback = vi.fn();

      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededProdOp}
          target={mockProductionTarget}
          onRollback={onRollback}
        />,
      );

      const rollbackBtn = screen.getByTestId("deployment-rollback-button");
      expect(rollbackBtn).toBeInTheDocument();

      await user.click(rollbackBtn);
      expect(onRollback).toHaveBeenCalledWith(
        mockSucceededProdOp,
        mockProductionTarget,
      );
    });

    it("hides rollback button when rollback capability is explicitly false", () => {
      const disabledRollbackTarget: DeploymentTarget = {
        ...mockProductionTarget,
        capabilities: {
          ...mockProductionTarget.capabilities!,
          rollback: false,
        },
      };

      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededProdOp}
          target={disabledRollbackTarget}
          onRollback={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("deployment-rollback-button"),
      ).not.toBeInTheDocument();
    });

    it("hides rollback button on non-production targets even if capability is true", () => {
      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededPreviewOp}
          target={{
            ...mockPreviewTarget,
            capabilities: {
              ...mockPreviewTarget.capabilities!,
              rollback: true,
            },
          }}
          onRollback={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("deployment-rollback-button"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Cancel action capability gating", () => {
    it("renders cancel button when operation is in active state and cancel capability is enabled", async () => {
      const user = userEvent.setup();
      const onCancel = vi.fn();

      renderWithI18n(
        <DeploymentDetailCard
          operation={mockRunningOp}
          target={mockPreviewTarget}
          onCancel={onCancel}
        />,
      );

      const cancelBtn = screen.getByTestId("deployment-cancel-button");
      expect(cancelBtn).toBeInTheDocument();

      await user.click(cancelBtn);
      expect(onCancel).toHaveBeenCalledWith(mockRunningOp);
    });

    it("hides cancel button when cancel capability is disabled", () => {
      const noCancelCapabilities: CapabilitySet = {
        ...mockPreviewTarget.capabilities!,
        cancel: false,
      };

      renderWithI18n(
        <DeploymentDetailCard
          operation={mockRunningOp}
          target={mockPreviewTarget}
          capabilities={noCancelCapabilities}
          onCancel={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("deployment-cancel-button"),
      ).not.toBeInTheDocument();
    });

    it("hides cancel button when operation is already ready or finished", () => {
      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededPreviewOp}
          target={mockPreviewTarget}
          onCancel={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("deployment-cancel-button"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Source revision links", () => {
    it("renders clickable commit link to git repository when commit SHA is present", () => {
      renderWithI18n(
        <DeploymentDetailCard
          operation={mockSucceededPreviewOp}
          target={mockPreviewTarget}
        />,
      );

      const revisionLink = screen.getByTestId(
        "deployment-source-revision-link",
      );
      expect(revisionLink).toBeInTheDocument();
      expect(revisionLink).toHaveAttribute(
        "href",
        "https://github.com/caglar09/multica/commit/abcdef1234567890",
      );
      expect(revisionLink).toHaveAttribute("target", "_blank");
      expect(screen.getByTestId("deployment-commit-sha")).toHaveTextContent(
        "abcdef1",
      );
    });
  });
});
