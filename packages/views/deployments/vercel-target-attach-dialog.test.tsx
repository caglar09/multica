import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { VercelTargetAttachDialog } from "./components/vercel-target-attach-dialog";
import type { DiscoveredVercelProject } from "@multica/core/types";

describe("VercelTargetAttachDialog", () => {
  const mockDiscovered: DiscoveredVercelProject[] = [
    {
      id: "prj_existing_1",
      name: "multica-web",
      framework: "nextjs",
      latest_deployment_url: "https://multica-web.vercel.app",
    },
  ];

  it("submits attached target when existing project is selected", async () => {
    const user = userEvent.setup();
    const handleSubmit = vi.fn();
    const handleOpenChange = vi.fn();

    renderWithI18n(
      <VercelTargetAttachDialog
        open={true}
        onOpenChange={handleOpenChange}
        connectionId="conn_123"
        projectId="project_abc"
        discoveredProjects={mockDiscovered}
        defaultEnvironment="production"
        onSubmitTarget={handleSubmit}
      />,
    );

    // Initial selected project is prj_existing_1
    const targetNameInput = screen.getByTestId("target-name-input");
    await user.type(targetNameInput, "Production Web");

    const submitBtn = screen.getByTestId("attach-dialog-submit");
    expect(submitBtn).toBeEnabled();

    await user.click(submitBtn);

    await waitFor(() => {
      expect(handleSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          connection_id: "conn_123",
          project_id: "project_abc",
          name: "Production Web",
          environment: "production",
          external_id: "prj_existing_1",
          config: expect.objectContaining({
            project_id: "prj_existing_1",
            project_name: "multica-web",
            framework: "custom",
          }),
        }),
      );
      expect(handleOpenChange).toHaveBeenCalledWith(false);
    });
  });

  it("uses repository-derived recipes and keeps recipe selection local", async () => {
    const user = userEvent.setup();
    const handleSubmit = vi.fn();

    renderWithI18n(
      <VercelTargetAttachDialog
        open={true}
        onOpenChange={vi.fn()}
        connectionId="conn_123"
        projectId="project_abc"
        discoveredProjects={mockDiscovered}
        repositoryMetadata={{
          files: ["apps/web/package.json", "apps/web/vite.config.ts"],
          dependencies: { vite: "6.0.0" },
          rootDirectories: ["apps/web"],
          environmentVariables: ["PUBLIC_API_URL=secret-value"],
        }}
        onSubmitTarget={handleSubmit}
      />,
    );

    expect(screen.getByTestId("framework-input")).toHaveValue("vite");
    expect(screen.getByTestId("root-dir-input")).toHaveValue("apps/web");
    expect(screen.getByTestId("environment-variable-names")).toHaveTextContent(
      "PUBLIC_API_URL",
    );
    expect(screen.getByTestId("environment-variable-names")).not.toHaveTextContent(
      "secret-value",
    );

    await user.click(screen.getByTestId("recipe-card-astro"));
    expect(handleSubmit).not.toHaveBeenCalled();

    await user.type(screen.getByTestId("target-name-input"), "Vite App");
    await user.click(screen.getByTestId("attach-dialog-submit"));
    expect(handleSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        config: expect.objectContaining({
          environment_variable_names: ["PUBLIC_API_URL"],
        }),
      }),
    );
    expect(JSON.stringify(handleSubmit.mock.calls[0]?.[0])).not.toContain(
      "secret-value",
    );
  });

  it("requires new project name and confirmation before enabling submit in create-new mode", async () => {
    const user = userEvent.setup();
    const handleSubmit = vi.fn();

    renderWithI18n(
      <VercelTargetAttachDialog
        open={true}
        onOpenChange={vi.fn()}
        connectionId="conn_123"
        projectId="project_abc"
        discoveredProjects={mockDiscovered}
        defaultEnvironment="preview"
        onSubmitTarget={handleSubmit}
      />,
    );

    // Toggle to create new mode
    const toggleBtn = screen.getByTestId("toggle-create-project-mode");
    await user.click(toggleBtn);

    const submitBtn = screen.getByTestId("attach-dialog-submit");
    expect(submitBtn).toBeDisabled();

    // Fill target name
    const targetNameInput = screen.getByTestId("target-name-input");
    await user.type(targetNameInput, "Preview App");

    // Fill new project name
    const newProjectInput = screen.getByTestId("new-vercel-project-input");
    await user.type(newProjectInput, "new-preview-service");

    // Still disabled because checkbox not checked
    expect(submitBtn).toBeDisabled();

    // Check confirmation checkbox
    const confirmCheckbox = screen.getByTestId("confirm-new-project-checkbox");
    await user.click(confirmCheckbox);

    // Now submit button is enabled
    expect(submitBtn).toBeEnabled();

    await user.click(submitBtn);

    await waitFor(() => {
      expect(handleSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          connection_id: "conn_123",
          project_id: "project_abc",
          name: "Preview App",
          environment: "preview",
          external_id: null,
          config: expect.objectContaining({
            project_name: "new-preview-service",
          }),
        }),
      );
    });
  });
});
