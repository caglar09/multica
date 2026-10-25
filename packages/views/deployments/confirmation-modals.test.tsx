import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { PromoteDeploymentConfirmDialog } from "./components/promote-deployment-confirm-dialog";
import { RollbackDeploymentConfirmDialog } from "./components/rollback-deployment-confirm-dialog";
import { DisconnectProviderConfirmDialog } from "./components/disconnect-provider-confirm-dialog";
import type {
  DeploymentOperation,
  DeploymentTarget,
  DeploymentProviderConnection,
} from "@multica/core/types";

describe("Deployment Confirmation Modals", () => {
  const mockTarget: DeploymentTarget = {
    id: "tgt_prod",
    workspace_id: "ws_1",
    connection_id: "conn_1",
    name: "Production Web",
    environment: "production",
    config: {
      project_name: "production-web",
    },
    created_at: "2026-09-13T00:00:00Z",
    updated_at: "2026-09-13T00:00:00Z",
  };

  const mockCandidateOp: DeploymentOperation = {
    id: "op_candidate",
    workspace_id: "ws_1",
    target_id: "tgt_prev",
    action: "deploy",
    idempotency_key: "idemp_1",
    status: "ready",
    attempt_count: 1,
    max_attempts: 3,
    request: {
      git_ref: "refs/heads/feature-candidate",
      commit_sha: "e33005fb816e642c2907b6953e19cb9",
      commit_message: "feat: ready for promote",
      environment: "preview",
      target_name: "Preview Staging",
    },
    evidence: {
      url: "https://preview.vercel.app",
    },
    created_at: "2026-09-13T10:00:00Z",
    updated_at: "2026-09-13T10:05:00Z",
  };

  const mockRollbackOp: DeploymentOperation = {
    id: "op_rollback_target",
    workspace_id: "ws_1",
    target_id: "tgt_prod",
    action: "deploy",
    idempotency_key: "idemp_2",
    status: "ready",
    attempt_count: 1,
    max_attempts: 3,
    request: {
      git_ref: "refs/heads/main",
      commit_sha: "9dc6b514c17d425abeb699f0d52248d",
      commit_message: "feat: last stable prod build",
      environment: "production",
      target_name: "Production Web",
    },
    evidence: {
      url: "https://production.vercel.app",
    },
    created_at: "2026-09-12T10:00:00Z",
    updated_at: "2026-09-12T10:05:00Z",
  };

  const mockConnection: DeploymentProviderConnection = {
    id: "conn_vercel_1",
    workspace_id: "ws_1",
    provider: "vercel",
    name: "Vercel Production",
    enabled: true,
    config: {
      team_slug: "acme-corp",
      account_name: "Acme Platform",
    },
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
  };

  describe("PromoteDeploymentConfirmDialog", () => {
    it("renders candidate commit SHA, branch, and requires confirmation checkbox before enabling promote", async () => {
      const user = userEvent.setup();
      const onConfirm = vi.fn();
      const onOpenChange = vi.fn();

      renderWithI18n(
        <PromoteDeploymentConfirmDialog
          open={true}
          onOpenChange={onOpenChange}
          candidateOperation={mockCandidateOp}
          target={mockTarget}
          targetName="Production Web"
          providerName="Vercel"
          onConfirm={onConfirm}
        />,
      );

      expect(
        screen.getByTestId("promote-deployment-dialog-title"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("promote-deployment-alert-banner"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("promote-deployment-context-grid"),
      ).toHaveTextContent("e33005f");
      expect(
        screen.getByTestId("promote-deployment-context-grid"),
      ).toHaveTextContent("refs/heads/feature-candidate");

      const confirmBtn = screen.getByTestId("promote-confirm-button");
      expect(confirmBtn).toBeDisabled();

      const checkbox = screen.getByTestId("promote-confirm-checkbox");
      await user.click(checkbox);
      expect(confirmBtn).toBeEnabled();

      await user.click(confirmBtn);
      await waitFor(() => {
        expect(onConfirm).toHaveBeenCalledWith(
          expect.stringMatching(/^promote_/),
        );
      });
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    it("cancels and calls onOpenChange(false) when cancel button is clicked", async () => {
      const user = userEvent.setup();
      const onOpenChange = vi.fn();

      renderWithI18n(
        <PromoteDeploymentConfirmDialog
          open={true}
          onOpenChange={onOpenChange}
          candidateOperation={mockCandidateOp}
          onConfirm={vi.fn()}
        />,
      );

      const cancelBtn = screen.getByTestId("cancel-promote-button");
      await user.click(cancelBtn);
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  describe("RollbackDeploymentConfirmDialog", () => {
    it("renders rollback commit SHA, warning banner, and requires checkbox before enabling rollback", async () => {
      const user = userEvent.setup();
      const onConfirm = vi.fn();
      const onOpenChange = vi.fn();

      renderWithI18n(
        <RollbackDeploymentConfirmDialog
          open={true}
          onOpenChange={onOpenChange}
          targetOperation={mockRollbackOp}
          target={mockTarget}
          targetName="Production Web"
          providerName="Vercel"
          onConfirm={onConfirm}
        />,
      );

      expect(
        screen.getByTestId("rollback-deployment-dialog-title"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("rollback-deployment-alert-banner"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("rollback-deployment-context-grid"),
      ).toHaveTextContent("9dc6b51");
      expect(
        screen.getByTestId("rollback-deployment-context-grid"),
      ).toHaveTextContent("feat: last stable prod build");

      const confirmBtn = screen.getByTestId("rollback-confirm-button");
      expect(confirmBtn).toBeDisabled();

      const checkbox = screen.getByTestId("rollback-confirm-checkbox");
      await user.click(checkbox);
      expect(confirmBtn).toBeEnabled();

      await user.click(confirmBtn);
      await waitFor(() => {
        expect(onConfirm).toHaveBeenCalledWith(
          expect.stringMatching(/^rollback_/),
        );
      });
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    it("cancels and calls onOpenChange(false) when cancel button is clicked", async () => {
      const user = userEvent.setup();
      const onOpenChange = vi.fn();

      renderWithI18n(
        <RollbackDeploymentConfirmDialog
          open={true}
          onOpenChange={onOpenChange}
          targetOperation={mockRollbackOp}
          onConfirm={vi.fn()}
        />,
      );

      const cancelBtn = screen.getByTestId("cancel-rollback-button");
      await user.click(cancelBtn);
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  describe("DisconnectProviderConfirmDialog", () => {
    it("renders provider connection details, target count warning, and requires confirmation checkbox", async () => {
      const user = userEvent.setup();
      const onConfirm = vi.fn();
      const onOpenChange = vi.fn();

      renderWithI18n(
        <DisconnectProviderConfirmDialog
          open={true}
          onOpenChange={onOpenChange}
          connection={mockConnection}
          targetCount={3}
          onConfirm={onConfirm}
        />,
      );

      expect(
        screen.getByTestId("disconnect-provider-dialog-title"),
      ).toHaveTextContent("Vercel Production");
      expect(
        screen.getByTestId("disconnect-provider-alert-banner"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("disconnect-provider-context"),
      ).toHaveTextContent("3 targets will be disabled");
      expect(
        screen.getByTestId("disconnect-provider-context"),
      ).toHaveTextContent("acme-corp");

      const confirmBtn = screen.getByTestId("disconnect-confirm-button");
      expect(confirmBtn).toBeDisabled();

      const checkbox = screen.getByTestId("disconnect-confirm-checkbox");
      await user.click(checkbox);
      expect(confirmBtn).toBeEnabled();

      await user.click(confirmBtn);
      await waitFor(() => {
        expect(onConfirm).toHaveBeenCalled();
      });
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    it("cancels and calls onOpenChange(false) when cancel button is clicked", async () => {
      const user = userEvent.setup();
      const onOpenChange = vi.fn();

      renderWithI18n(
        <DisconnectProviderConfirmDialog
          open={true}
          onOpenChange={onOpenChange}
          connection={mockConnection}
          onConfirm={vi.fn()}
        />,
      );

      const cancelBtn = screen.getByTestId("cancel-disconnect-button");
      await user.click(cancelBtn);
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });
});
