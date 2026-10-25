import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { TargetEnvironmentConfig } from "./components/target-environment-config";

describe("TargetEnvironmentConfig", () => {
  it("allows setting target name and git branch", async () => {
    const user = userEvent.setup();
    const handleNameChange = vi.fn();
    const handleBranchChange = vi.fn();

    renderWithI18n(
      <TargetEnvironmentConfig
        targetName="Preview Target"
        onChangeTargetName={handleNameChange}
        environment="preview"
        onChangeEnvironment={vi.fn()}
        gitBranch="feat/test"
        onChangeGitBranch={handleBranchChange}
      />,
    );

    const nameInput = screen.getByTestId("target-name-input");
    expect(nameInput).toHaveValue("Preview Target");

    const branchInput = screen.getByTestId("git-branch-input");
    expect(branchInput).toHaveValue("feat/test");

    await user.type(nameInput, "!");
    expect(handleNameChange).toHaveBeenCalled();
  });

  it("displays Level 2 production warning only when environment is production", async () => {
    const user = userEvent.setup();
    const handleEnvChange = vi.fn();

    const { rerender } = renderWithI18n(
      <TargetEnvironmentConfig
        targetName="Production Target"
        onChangeTargetName={vi.fn()}
        environment="preview"
        onChangeEnvironment={handleEnvChange}
        gitBranch="main"
        onChangeGitBranch={vi.fn()}
      />,
    );

    expect(
      screen.queryByTestId("production-environment-guard-warning"),
    ).not.toBeInTheDocument();

    const prodCard = screen.getByTestId("env-card-production");
    await user.click(prodCard);
    expect(handleEnvChange).toHaveBeenCalledWith("production");

    // Re-render as production
    rerender(
      <TargetEnvironmentConfig
        targetName="Production Target"
        onChangeTargetName={vi.fn()}
        environment="production"
        onChangeEnvironment={handleEnvChange}
        gitBranch="main"
        onChangeGitBranch={vi.fn()}
      />,
    );

    expect(
      screen.getByTestId("production-environment-guard-warning"),
    ).toBeInTheDocument();
  });
});
