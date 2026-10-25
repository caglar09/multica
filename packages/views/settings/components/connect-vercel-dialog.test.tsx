import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { ConnectVercelDialog } from "./connect-vercel-dialog";

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));

vi.mock("@multica/core/deployments/mutations", () => ({
  useCreateDeploymentConnection: () => ({
    mutateAsync: mocks.create,
    isPending: false,
  }),
}));

vi.mock("sonner", () => ({
  toast: { success: mocks.success, error: mocks.error },
}));

function OpenDialog() {
  const [open, setOpen] = useState(true);
  return <ConnectVercelDialog open={open} onOpenChange={setOpen} />;
}

describe("ConnectVercelDialog", () => {
  beforeEach(() => {
    mocks.create.mockReset().mockResolvedValue({ id: "conn-1" });
    mocks.success.mockReset();
    mocks.error.mockReset();
  });

  it("validates the connection name and PAT before calling the API", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OpenDialog />);

    await user.click(screen.getByTestId("vercel-connect-submit"));

    expect(screen.getByTestId("vercel-name-error")).toBeInTheDocument();
    expect(screen.getByTestId("vercel-token-error")).toBeInTheDocument();
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it("submits the generic Vercel connection payload and reports success", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OpenDialog />);

    await user.type(screen.getByTestId("vercel-connection-name"), "Production");
    await user.type(screen.getByTestId("vercel-pat"), "vercel_pat_test_secret");
    await user.type(screen.getByTestId("vercel-team-id"), "team-1");
    await user.type(screen.getByTestId("vercel-team-slug"), "multica");
    await user.click(screen.getByTestId("vercel-connect-submit"));

    await waitFor(() => expect(mocks.create).toHaveBeenCalledWith({
      provider: "vercel",
      name: "Production",
      token: "vercel_pat_test_secret",
      config: { team_id: "team-1", team_slug: "multica" },
    }));
    expect(mocks.success).toHaveBeenCalledWith("Vercel connected successfully");
    await waitFor(() =>
      expect(screen.queryByTestId("connect-vercel-dialog-content")).not.toBeInTheDocument(),
    );
  });

  it("shows a safe error without echoing the PAT", async () => {
    const user = userEvent.setup();
    const token = "vercel_pat_error_secret";
    mocks.create.mockRejectedValue(new Error(`provider rejected ${token}`));
    renderWithI18n(<OpenDialog />);

    await user.type(screen.getByTestId("vercel-connection-name"), "Production");
    await user.type(screen.getByTestId("vercel-pat"), token);
    await user.click(screen.getByTestId("vercel-connect-submit"));

    const error = await screen.findByTestId("connect-vercel-error");
    expect(error).toHaveTextContent("Could not connect Vercel");
    expect(error).not.toHaveTextContent(token);
    expect(document.body).not.toHaveTextContent(token);
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("does not write the PAT into rendered connection state after success", async () => {
    const user = userEvent.setup();
    const token = "vercel_pat_regression_secret";
    mocks.create.mockResolvedValue({
      id: "conn-1",
      provider: "vercel",
      name: "Production",
      token,
    });
    renderWithI18n(<OpenDialog />);

    await user.type(screen.getByTestId("vercel-connection-name"), "Production");
    await user.type(screen.getByTestId("vercel-pat"), token);
    await user.click(screen.getByTestId("vercel-connect-submit"));

    await waitFor(() => expect(screen.queryByDisplayValue(token)).not.toBeInTheDocument());
    expect(document.body).not.toHaveTextContent(token);
    expect(mocks.success).toHaveBeenCalledWith("Vercel connected successfully");
  });
});
