import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  deploymentKeys,
  deploymentConnectionsOptions,
  deploymentTargetsOptions,
  discoveredVercelProjectsOptions,
} from "./queries";

vi.mock("../api", () => ({
  api: {
    listDeploymentConnections: vi.fn(),
    listDiscoveredVercelProjects: vi.fn(),
    listDeploymentTargets: vi.fn(),
    listDeploymentOperations: vi.fn(),
    getDeploymentOperation: vi.fn(),
  },
}));

describe("Deployment Queries", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("builds consistent query keys", () => {
    expect(deploymentKeys.all("ws-1")).toEqual(["deployments", "ws-1"]);
    expect(deploymentKeys.connections("ws-1")).toEqual([
      "deployments",
      "ws-1",
      "connections",
    ]);
    expect(deploymentKeys.discoveredProjects("ws-1", "conn-1")).toEqual([
      "deployments",
      "ws-1",
      "connections",
      "conn-1",
      "projects",
    ]);
    expect(deploymentKeys.targets("ws-1", "proj-1")).toEqual([
      "deployments",
      "ws-1",
      "targets",
      "proj-1",
    ]);
    expect(deploymentKeys.operations("ws-1", "target-1")).toEqual([
      "deployments",
      "ws-1",
      "operations",
      "target-1",
    ]);
  });

  it("wires query functions properly", async () => {
    const connOptions = deploymentConnectionsOptions("ws-1");
    expect(connOptions.queryKey).toEqual([
      "deployments",
      "ws-1",
      "connections",
    ]);
    expect(connOptions.enabled).toBe(true);

    const discOptions = discoveredVercelProjectsOptions("ws-1", "conn-1");
    expect(discOptions.queryKey).toEqual([
      "deployments",
      "ws-1",
      "connections",
      "conn-1",
      "projects",
    ]);
    expect(discOptions.enabled).toBe(true);

    const targetOptions = deploymentTargetsOptions("ws-1", "proj-1");
    expect(targetOptions.queryKey).toEqual([
      "deployments",
      "ws-1",
      "targets",
      "proj-1",
    ]);
  });
});
