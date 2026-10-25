import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { ProductionDeployConfirmDialog } from "./components/production-deploy-confirm-dialog";

describe("ProductionDeployConfirmDialog", () => {
  const defaultProps = {
    open: true,
    onOpenChange: vi.fn(),
    targetName: "Production Web",
    providerName: "Vercel",
    teamSlug: "my-team",
    commitSha: "e33005fb816e642c2907b6953e19cb9",
    gitRef: "refs/heads/main",
    commitMessage: "feat: deploy providers",
    initiatorName: "Frontend Engineer",
    environment: "production",
    onConfirm: vi.fn(),
  };

  it("renders dedicated production confirmation with exact commit SHA and environment", () => {
    renderWithI18n(<ProductionDeployConfirmDialog {...defaultProps} />);

    expect(
      screen.getByTestId("production-deploy-dialog-title"),
    ).toBeInTheDocument();

    const banner = screen.getByTestId("production-deploy-alert-banner");
    expect(banner).toBeInTheDocument();
    expect(banner).toHaveTextContent("e33005f");
    expect(banner).toHaveTextContent("Production Web");

    const grid = screen.getByTestId("production-deploy-context-grid");
    expect(grid).toHaveTextContent("Production Web (production)");
    expect(grid).toHaveTextContent("Vercel (Team: my-team)");
    expect(grid).toHaveTextContent("refs/heads/main (e33005f)");
    expect(grid).toHaveTextContent("Frontend Engineer");
  });

  it("enforces confirmation checkbox before enabling confirm button", async () => {
    const user = userEvent.setup();
    const handleConfirm = vi.fn();

    renderWithI18n(
      <ProductionDeployConfirmDialog
        {...defaultProps}
        onConfirm={handleConfirm}
      />,
    );

    const confirmBtn = screen.getByTestId("confirm-deploy-button");
    expect(confirmBtn).toBeDisabled();

    const checkbox = screen.getByTestId("production-deploy-checkbox");
    await user.click(checkbox);

    expect(confirmBtn).toBeEnabled();

    await user.click(confirmBtn);
    await waitFor(() => {
      expect(handleConfirm).toHaveBeenCalledWith(
        expect.stringMatching(/^appr_/),
      );
    });
  });

  it("calls onOpenChange(false) when cancel button is clicked", async () => {
    const user = userEvent.setup();
    const handleOpenChange = vi.fn();

    renderWithI18n(
      <ProductionDeployConfirmDialog
        {...defaultProps}
        onOpenChange={handleOpenChange}
      />,
    );

    const cancelBtn = screen.getByTestId("cancel-deploy-button");
    await user.click(cancelBtn);

    expect(handleOpenChange).toHaveBeenCalledWith(false);
  });
});
