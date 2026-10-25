import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { DeploymentProvidersTab } from "./deployment-providers-tab";
import type {
  DeploymentProviderConnection,
  DeploymentTarget,
} from "@multica/core/types";

const mockDisconnectMutateAsync = vi.fn();
const mockCreateMutateAsync = vi.fn();
const mockWebhookMutateAsync = vi.fn();
const mockState = {
  role: "admin",
  connections: [] as DeploymentProviderConnection[],
  targets: [] as DeploymentTarget[],
};

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws_1",
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({
    role: mockState.role,
    user_id: "user_1",
    workspace_id: "ws_1",
  }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: readonly unknown[] }) => {
    const key = options.queryKey?.[0];
    if (key === "deployment-connections") {
      return { data: mockState.connections, isLoading: false };
    }
    if (key === "deployment-targets") {
      return { data: mockState.targets, isLoading: false };
    }
    return { data: undefined, isLoading: false };
  },
}));

vi.mock("@multica/core/deployments/queries", () => ({
  deploymentConnectionsOptions: (wsId: string) => ({
    queryKey: ["deployment-connections", wsId],
  }),
  deploymentTargetsOptions: (wsId: string) => ({
    queryKey: ["deployment-targets", wsId],
  }),
}));

vi.mock("@multica/core/deployments/mutations", () => ({
  useCreateDeploymentConnection: () => ({
    mutateAsync: mockCreateMutateAsync,
    isPending: false,
  }),
  useDisconnectDeploymentConnection: () => ({
    mutateAsync: mockDisconnectMutateAsync,
    isPending: false,
  }),
  useSetDeploymentWebhookSecret: () => ({
    mutateAsync: mockWebhookMutateAsync,
    isPending: false,
  }),
}));

describe("DeploymentProvidersTab", () => {
  const sampleConnection: DeploymentProviderConnection = {
    id: "conn_vercel_prod",
    workspace_id: "ws_1",
    provider: "vercel",
    name: "Production Vercel Account",
    enabled: true,
    config: {
      team_slug: "multica-team",
      account_name: "Multica Platform",
    },
    webhook_url: "/api/webhooks/deployments/ws_1/conn_vercel_prod",
    webhook_configured: false,
    webhook_status: "polling_only",
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
  };

  const sampleTarget: DeploymentTarget = {
    id: "tgt_prod_1",
    workspace_id: "ws_1",
    connection_id: "conn_vercel_prod",
    name: "Web Prod",
    environment: "production",
    config: {
      project_name: "web-prod",
    },
    created_at: "2026-09-11T00:00:00Z",
    updated_at: "2026-09-11T00:00:00Z",
  };

  beforeEach(() => {
    mockState.role = "admin";
    mockState.connections = [];
    mockState.targets = [];
    mockDisconnectMutateAsync.mockReset().mockResolvedValue({ success: true });
    mockCreateMutateAsync.mockReset();
    mockWebhookMutateAsync.mockReset().mockResolvedValue({});
  });

  it("renders empty state placeholder when no provider is connected", () => {
    renderWithI18n(<DeploymentProvidersTab />);

    expect(
      screen.getByTestId("empty-connections-placeholder"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("available-provider-card-vercel"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("available-provider-card-render"),
    ).toBeInTheDocument();
  });

  it("renders active connection details, status badge, and target count", () => {
    mockState.connections = [sampleConnection];
    mockState.targets = [sampleTarget];

    renderWithI18n(<DeploymentProvidersTab />);

    expect(screen.getByTestId("active-connections-list")).toBeInTheDocument();
    expect(
      screen.getByTestId("connection-card-conn_vercel_prod"),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId("connection-status-badge-conn_vercel_prod"),
    ).toHaveTextContent("Connected");

    const card = screen.getByTestId("connection-card-conn_vercel_prod");
    expect(card).toHaveTextContent("Production Vercel Account");
    expect(card).toHaveTextContent("multica-team");
    expect(card).toHaveTextContent("1 targets attached");
    expect(screen.getByTestId("connection-webhook-url-conn_vercel_prod")).toHaveTextContent(
      "/api/webhooks/deployments/ws_1/conn_vercel_prod",
    );
    expect(screen.getByTestId("connection-card-conn_vercel_prod")).toHaveTextContent("Polling only");
    expect(screen.getByTestId("configure-webhook-button-conn_vercel_prod")).toHaveTextContent("Configure webhook");
  });

  it("opens disconnect confirmation dialog and invokes mutation on confirmation", async () => {
    const user = userEvent.setup();
    mockState.connections = [sampleConnection];
    mockState.targets = [sampleTarget];

    renderWithI18n(<DeploymentProvidersTab />);

    const disconnectBtn = screen.getByTestId(
      "disconnect-connection-button-conn_vercel_prod",
    );
    expect(disconnectBtn).toBeInTheDocument();

    await user.click(disconnectBtn);

    expect(
      screen.getByTestId("disconnect-provider-dialog-content"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("disconnect-provider-context")).toHaveTextContent(
      "1 targets will be disabled",
    );

    const confirmBtn = screen.getByTestId("disconnect-confirm-button");
    expect(confirmBtn).toBeDisabled();

    const checkbox = screen.getByTestId("disconnect-confirm-checkbox");
    await user.click(checkbox);
    expect(confirmBtn).toBeEnabled();

    await user.click(confirmBtn);

    await waitFor(() => {
      expect(mockDisconnectMutateAsync).toHaveBeenCalledWith(
        "conn_vercel_prod",
      );
    });
  });

  it("configures a webhook secret separately from connection creation", async () => {
    const user = userEvent.setup();
    mockState.connections = [sampleConnection];
    renderWithI18n(<DeploymentProvidersTab />);

    await user.click(screen.getByTestId("configure-webhook-button-conn_vercel_prod"));
    await user.type(screen.getByTestId("vercel-webhook-secret"), "webhook-secret");
    await user.click(screen.getByTestId("vercel-webhook-save"));

    await waitFor(() => expect(mockWebhookMutateAsync).toHaveBeenCalledWith({
      connectionId: "conn_vercel_prod",
      webhookSecret: "webhook-secret",
    }));
  });

  it("hides management actions for regular members without admin or owner role", () => {
    mockState.role = "member";
    mockState.connections = [sampleConnection];

    renderWithI18n(<DeploymentProvidersTab />);

    expect(
      screen.queryByTestId("disconnect-connection-button-conn_vercel_prod"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("connect-provider-render"),
    ).not.toBeInTheDocument();
  });

  it("opens the Vercel PAT form from the provider card", async () => {
    const user = userEvent.setup();
    renderWithI18n(<DeploymentProvidersTab />);

    await user.click(screen.getByTestId("connect-provider-vercel"));

    expect(screen.getByTestId("connect-vercel-dialog-content")).toBeInTheDocument();
    expect(screen.getByTestId("vercel-pat")).toHaveAttribute("type", "password");
    expect(
      within(screen.getByTestId("available-provider-card-render")).getByText(
        "Coming soon",
      ),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("available-provider-card-railway")).getByText(
        "Coming soon",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("connect-provider-render")).not.toBeInTheDocument();
    expect(screen.queryByTestId("connect-provider-railway")).not.toBeInTheDocument();
  });
});
