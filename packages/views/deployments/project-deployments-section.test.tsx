import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { deploymentKeys } from "@multica/core/deployments/queries";
import { renderWithI18n } from "../test/i18n";
import { ProjectDeploymentsSection } from "./components/project-deployments-section";
import type {
  DeploymentTarget,
  DeploymentOperation,
} from "@multica/core/types";

const mockMutateTrigger = vi.hoisted(() => vi.fn());
const mockMutateCreate = vi.hoisted(() => vi.fn());
const mockMutateCreateProvider = vi.hoisted(() => vi.fn());
const mockMutatePromote = vi.hoisted(() => vi.fn());
const mockMutateRollback = vi.hoisted(() => vi.fn());
const mockMutateCancel = vi.hoisted(() => vi.fn());
const mockMutateDelete = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
}));

vi.mock("@multica/core/deployments/mutations", () => ({
  useCreateDeploymentTarget: () => ({
    mutateAsync: mockMutateCreate,
  }),
  useCreateDeploymentProviderTarget: () => ({
    mutateAsync: mockMutateCreateProvider,
  }),
  useTriggerDeployment: () => ({
    mutate: mockMutateTrigger,
    mutateAsync: mockMutateTrigger,
  }),
  usePromoteDeployment: () => ({
    mutate: mockMutatePromote,
    mutateAsync: mockMutatePromote,
  }),
  useRollbackDeployment: () => ({
    mutate: mockMutateRollback,
    mutateAsync: mockMutateRollback,
  }),
  useCancelDeployment: () => ({
    mutate: mockMutateCancel,
    mutateAsync: mockMutateCancel,
  }),
  useDeleteDeploymentTarget: () => ({
    mutate: mockMutateDelete,
    isPending: false,
  }),
}));

describe("ProjectDeploymentsSection", () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  const mockTargets: DeploymentTarget[] = [
    {
      id: "tgt_preview",
      workspace_id: "ws-test",
      name: "Preview Target",
      environment: "preview",
      connection_id: "conn_1",
      config: { project_name: "web-app", framework: "nextjs" },
      created_at: "2026-09-13T00:00:00Z",
      updated_at: "2026-09-13T00:00:00Z",
    },
    {
      id: "tgt_prod",
      workspace_id: "ws-test",
      name: "Production Target",
      environment: "production",
      connection_id: "conn_1",
      config: { project_name: "web-app", framework: "nextjs" },
      created_at: "2026-09-13T00:00:00Z",
      updated_at: "2026-09-13T00:00:00Z",
    },
  ];

  const mockOperations: DeploymentOperation[] = [
    {
      id: "op_1",
      workspace_id: "ws-test",
      target_id: "tgt_prod",
      action: "deploy",
      idempotency_key: "idemp_1",
      status: "ready",
      attempt_count: 1,
      max_attempts: 3,
      request: {
        git_ref: "refs/heads/main",
        commit_sha: "c82e4ff816e642c2907b6953e19cb9",
        environment: "production",
        target_name: "Production Target",
      },
      evidence: {
        url: "https://web-app-prod.vercel.app",
        inspector_url: "https://vercel.com/team/web-app/deployments/dpl_1",
      },
      created_at: "2026-09-13T10:00:00Z",
      updated_at: "2026-09-13T10:05:00Z",
    },
  ];

  it("renders targets and recent deployment cards with external links", () => {
    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <ProjectDeploymentsSection
          projectId="p_1"
          targets={mockTargets}
          operations={mockOperations}
          repository="https://github.com/caglar09/clock-time"
          currentCommitSha="c82e4ff816e642c2907b6953e19cb9"
          currentGitRef="main"
          currentCommitMessage="fix: deploy the selected source revision"
        />
      </QueryClientProvider>,
    );

    expect(screen.getByText("Preview Target")).toBeInTheDocument();
    expect(
      screen.getAllByText("Production Target").length,
    ).toBeGreaterThanOrEqual(1);
    expect(screen.getByTestId("external-deployment-url")).toHaveAttribute(
      "href",
      "https://web-app-prod.vercel.app",
    );
    expect(screen.getByTestId("external-dashboard-url")).toHaveAttribute(
      "href",
      "https://vercel.com/team/web-app",
    );
  });

  it("loads deployment history for attached targets", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    client.setQueryData(
      deploymentKeys.operations("ws-test", "tgt_prod"),
      mockOperations,
    );

    renderWithI18n(
      <QueryClientProvider client={client}>
        <ProjectDeploymentsSection
          projectId="p_1"
          targets={[mockTargets[1]!]}
          repository="https://github.com/caglar09/clock-time"
          currentGitRef="main"
        />
      </QueryClientProvider>,
    );

    expect(await screen.findByTestId("deployment-operations-list")).toBeInTheDocument();
    expect(screen.getByTestId("deployment-detail-card")).toBeInTheDocument();
  });

  it("triggers preview deployment immediately without modal", async () => {
    const user = userEvent.setup();

    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <ProjectDeploymentsSection
          projectId="p_1"
          targets={mockTargets}
          operations={mockOperations}
          repository="https://github.com/caglar09/clock-time"
          currentCommitSha="c82e4ff816e642c2907b6953e19cb9"
          currentGitRef="main"
          currentCommitMessage="fix: deploy the selected source revision"
        />
      </QueryClientProvider>,
    );

    const triggerPreviewBtn = screen.getByTestId("trigger-deploy-tgt_preview");
    await user.click(triggerPreviewBtn);

    expect(mockMutateTrigger).toHaveBeenCalledWith(
      expect.objectContaining({
        target_id: "tgt_preview",
        environment: "preview",
      }),
    );
  });

  it("opens dedicated production confirmation dialog when triggering production deployment", async () => {
    const user = userEvent.setup();

    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <ProjectDeploymentsSection
          projectId="p_1"
          targets={mockTargets}
          operations={mockOperations}
          repository="https://github.com/caglar09/clock-time"
          currentCommitSha="c82e4ff816e642c2907b6953e19cb9"
          currentGitRef="main"
          currentCommitMessage="fix: deploy the selected source revision"
        />
      </QueryClientProvider>,
    );

    const triggerProdBtn = screen.getByTestId("trigger-deploy-tgt_prod");
    await user.click(triggerProdBtn);

    // Confirmation dialog should appear
    expect(
      screen.getByTestId("production-deploy-dialog-title"),
    ).toBeInTheDocument();
    expect(screen.getByText("main (c82e4ff)")).toBeInTheDocument();
    expect(screen.getByTestId("confirm-deploy-button")).toBeDisabled();

    // Check checkbox
    await user.click(screen.getByTestId("production-deploy-checkbox"));
    expect(screen.getByTestId("confirm-deploy-button")).toBeEnabled();

    // Confirm deploy
    await user.click(screen.getByTestId("confirm-deploy-button"));

    await waitFor(() => {
      expect(mockMutateTrigger).toHaveBeenCalledWith(
        expect.objectContaining({
          target_id: "tgt_prod",
          environment: "production",
          commit_sha: "c82e4ff816e642c2907b6953e19cb9",
          approval_reference: expect.stringMatching(/^appr_/),
        }),
      );
    });
  });

  it("does not trigger deployment without a source revision", async () => {
    const user = userEvent.setup();
    mockMutateTrigger.mockClear();

    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <ProjectDeploymentsSection
          projectId="p_1"
          targets={mockTargets}
          operations={[]}
        />
      </QueryClientProvider>,
    );

    const triggerPreviewBtn = screen.getByTestId("trigger-deploy-tgt_preview");
    expect(triggerPreviewBtn).toBeDisabled();
    expect(screen.getByTestId("source-revision-unavailable")).toHaveTextContent(
      "Source revision unavailable",
    );
    await user.click(triggerPreviewBtn);
    expect(mockMutateTrigger).not.toHaveBeenCalled();
  });

  it("deploys the target branch when an exact commit is unavailable", async () => {
    const user = userEvent.setup();
    mockMutateTrigger.mockClear();
    const target: DeploymentTarget = {
      ...mockTargets[0]!,
      config: {
        ...mockTargets[0]!.config,
        repository: "https://github.com/caglar09/clock-time.git",
        default_branch: "main",
      },
    };

    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <ProjectDeploymentsSection
          projectId="p_1"
          targets={[target]}
          operations={[]}
        />
      </QueryClientProvider>,
    );

    const trigger = screen.getByTestId("trigger-deploy-tgt_preview");
    expect(trigger).toBeEnabled();
    await user.click(trigger);

    expect(mockMutateTrigger).toHaveBeenCalledWith(
      expect.objectContaining({
        repository: "https://github.com/caglar09/clock-time.git",
        git_ref: "main",
        commit_sha: undefined,
      }),
    );
  });

  it("unlinks a target only after confirmation", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);

    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <ProjectDeploymentsSection
          projectId="p_1"
          targets={mockTargets}
          operations={mockOperations}
        />
      </QueryClientProvider>,
    );

    await user.click(screen.getByTestId("unlink-target-tgt_preview"));

    expect(confirm).toHaveBeenCalled();
    expect(mockMutateDelete).toHaveBeenCalledWith("tgt_preview");
    confirm.mockRestore();
  });
});
