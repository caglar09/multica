import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DeploymentProviderConnection } from "../types/deployment";
import { deploymentKeys } from "./queries";
import { useCreateDeploymentConnection } from "./mutations";

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  invalidate: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useMutation: (options: unknown) => options,
  useQueryClient: () => ({ invalidateQueries: mocks.invalidate }),
}));

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../api", () => ({
  api: { createDeploymentConnection: mocks.create },
}));

describe("useCreateDeploymentConnection", () => {
  beforeEach(() => {
    mocks.create.mockReset();
    mocks.invalidate.mockReset();
  });

  it("calls the generic API and invalidates connections and targets", async () => {
    const connection = {
      id: "conn-1",
      workspace_id: "ws-1",
      provider: "vercel",
      name: "Production",
      enabled: true,
      created_at: "2026-09-14T00:00:00Z",
      updated_at: "2026-09-14T00:00:00Z",
    } satisfies DeploymentProviderConnection;
    mocks.create.mockResolvedValue(connection);

    const input = {
      provider: "vercel" as const,
      name: "Production",
      token: "vercel_pat_test_secret",
      webhook_secret: "vercel_webhook_secret",
      config: { team_id: "team-1", team_slug: "multica" },
    };
    const mutation = useCreateDeploymentConnection() as unknown as {
      mutationFn: (data: typeof input) => Promise<DeploymentProviderConnection>;
      onSuccess: (data: DeploymentProviderConnection) => void;
    };

    const result = await mutation.mutationFn(input);
    mutation.onSuccess(result);

    expect(mocks.create).toHaveBeenCalledWith("ws-1", input);
    expect(mocks.invalidate).toHaveBeenNthCalledWith(1, {
      queryKey: deploymentKeys.connections("ws-1"),
    });
    expect(mocks.invalidate).toHaveBeenNthCalledWith(2, {
      queryKey: deploymentKeys.targets("ws-1"),
    });
  });
});
