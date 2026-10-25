import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../test/i18n";
import { VercelProjectSelect } from "./components/vercel-project-select";
import type { DiscoveredVercelProject } from "@multica/core/types";

const mockProjects: DiscoveredVercelProject[] = [
  {
    id: "prj_1",
    name: "web-frontend",
    framework: "nextjs",
    latest_deployment_url: "https://web-frontend.vercel.app",
  },
  {
    id: "prj_2",
    name: "docs-portal",
    framework: "astro",
    latest_deployment_url: "https://docs-portal.vercel.app",
  },
];

describe("VercelProjectSelect", () => {
  it("renders existing projects list and allows selection", async () => {
    const user = userEvent.setup();
    const handleSelect = vi.fn();

    renderWithI18n(
      <VercelProjectSelect
        projects={mockProjects}
        selectedProjectId={null}
        onSelectProject={handleSelect}
        newProjectName=""
        onChangeNewProjectName={vi.fn()}
        isCreatingNew={false}
        onToggleCreatingNew={vi.fn()}
        isConfirmedNew={false}
        onConfirmNew={vi.fn()}
      />,
    );

    expect(screen.getByText("web-frontend")).toBeInTheDocument();
    expect(screen.getByText("docs-portal")).toBeInTheDocument();

    const firstItem = screen.getByTestId("vercel-project-item-prj_1");
    await user.click(firstItem);
    expect(handleSelect).toHaveBeenCalledWith(mockProjects[0]);
  });

  it("filters projects when searching", async () => {
    const user = userEvent.setup();

    renderWithI18n(
      <VercelProjectSelect
        projects={mockProjects}
        selectedProjectId={null}
        onSelectProject={vi.fn()}
        newProjectName=""
        onChangeNewProjectName={vi.fn()}
        isCreatingNew={false}
        onToggleCreatingNew={vi.fn()}
        isConfirmedNew={false}
        onConfirmNew={vi.fn()}
      />,
    );

    const searchInput = screen.getByTestId("search-vercel-projects");
    await user.type(searchInput, "docs");

    expect(screen.getByText("docs-portal")).toBeInTheDocument();
    expect(screen.queryByText("web-frontend")).not.toBeInTheDocument();
  });

  it("toggles to create new project mode with explicit confirmation requirement", async () => {
    const user = userEvent.setup();
    const handleToggle = vi.fn();
    const handleNameChange = vi.fn();
    const handleConfirmNew = vi.fn();

    const { rerender } = renderWithI18n(
      <VercelProjectSelect
        projects={mockProjects}
        selectedProjectId={null}
        onSelectProject={vi.fn()}
        newProjectName=""
        onChangeNewProjectName={handleNameChange}
        isCreatingNew={false}
        onToggleCreatingNew={handleToggle}
        isConfirmedNew={false}
        onConfirmNew={handleConfirmNew}
      />,
    );

    const toggleBtn = screen.getByTestId("toggle-create-project-mode");
    await user.click(toggleBtn);
    expect(handleToggle).toHaveBeenCalledWith(true);

    // Re-render in creating mode
    rerender(
      <VercelProjectSelect
        projects={mockProjects}
        selectedProjectId={null}
        onSelectProject={vi.fn()}
        newProjectName="brand-new-app"
        onChangeNewProjectName={handleNameChange}
        isCreatingNew={true}
        onToggleCreatingNew={handleToggle}
        isConfirmedNew={false}
        onConfirmNew={handleConfirmNew}
      />,
    );

    expect(screen.getByTestId("new-vercel-project-input")).toHaveValue(
      "brand-new-app",
    );
    expect(
      screen.getByTestId("confirm-new-project-checkbox"),
    ).toBeInTheDocument();

    const checkbox = screen.getByTestId("confirm-new-project-checkbox");
    await user.click(checkbox);
    expect(handleConfirmNew).toHaveBeenCalledWith(true);
  });
});
