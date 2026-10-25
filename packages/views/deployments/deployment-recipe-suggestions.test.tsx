import { useState } from "react";
import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { DeploymentRecipeSuggestions } from "./components/deployment-recipe-suggestions";
import type { VercelTargetConfig } from "@multica/core/types";

describe("DeploymentRecipeSuggestions", () => {
  const initialConfig: VercelTargetConfig = {
    framework: "nextjs",
    root_directory: "",
    build_command: "next build",
    output_directory: ".next",
    install_command: "pnpm install",
    dev_command: "next dev",
  };

  it("renders framework presets and selects a preset", async () => {
    const user = userEvent.setup();
    const handleSelectRecipe = vi.fn();
    const handleChangeConfig = vi.fn();

    renderWithI18n(
      <DeploymentRecipeSuggestions
        selectedFramework="nextjs"
        onSelectRecipe={handleSelectRecipe}
        config={initialConfig}
        onChangeConfig={handleChangeConfig}
      />,
    );

    expect(screen.getByTestId("recipe-card-nextjs")).toBeInTheDocument();
    expect(screen.getByTestId("recipe-card-vite")).toBeInTheDocument();
    expect(screen.getByTestId("recipe-card-remix")).toBeInTheDocument();

    const viteCard = screen.getByTestId("recipe-card-vite");
    await user.click(viteCard);

    expect(handleSelectRecipe).toHaveBeenCalledWith(
      expect.objectContaining({ framework: "vite" }),
    );
    expect(handleChangeConfig).toHaveBeenCalledWith(
      expect.objectContaining({
        framework: "vite",
        build_command: "npm run build",
        output_directory: "dist",
      }),
    );
  });

  it("supports editable overrides for all build settings", async () => {
    const user = userEvent.setup();

    function TestHarness() {
      const [config, setConfig] = useState<VercelTargetConfig>(initialConfig);
      const [framework, setFramework] = useState("nextjs");

      return (
        <div>
          <DeploymentRecipeSuggestions
            selectedFramework={framework}
            onSelectRecipe={(r) => setFramework(r.framework)}
            config={config}
            onChangeConfig={setConfig}
          />
          <span data-testid="live-root-dir">{config.root_directory}</span>
          <span data-testid="live-build-command">{config.build_command}</span>
        </div>
      );
    }

    renderWithI18n(<TestHarness />);

    const rootDirInput = screen.getByTestId("root-dir-input");
    await user.type(rootDirInput, "apps/web");
    expect(screen.getByTestId("live-root-dir")).toHaveTextContent("apps/web");

    const buildCommandInput = screen.getByTestId("build-command-input");
    await user.clear(buildCommandInput);
    await user.type(buildCommandInput, "turbo run build");
    expect(screen.getByTestId("live-build-command")).toHaveTextContent(
      "turbo run build",
    );
  });
});
