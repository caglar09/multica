import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type {
  DeploymentProviderConnection,
  DiscoveredVercelProject,
  DeploymentTarget,
  DeploymentOperation,
  DeploymentLogResult,
  DeploymentSourceRevision,
} from "../types/deployment";

export const deploymentKeys = {
  all: (wsId: string) => ["deployments", wsId] as const,
  connections: (wsId: string) =>
    [...deploymentKeys.all(wsId), "connections"] as const,
  discoveredProjects: (wsId: string, connectionId: string) =>
    [
      ...deploymentKeys.all(wsId),
      "connections",
      connectionId,
      "projects",
    ] as const,
  targets: (wsId: string, projectId?: string) =>
    [...deploymentKeys.all(wsId), "targets", projectId ?? "all"] as const,
  target: (wsId: string, targetId: string) =>
    [...deploymentKeys.all(wsId), "targets", "detail", targetId] as const,
  operations: (wsId: string, targetId: string) =>
    [...deploymentKeys.all(wsId), "operations", targetId] as const,
  operation: (wsId: string, operationId: string) =>
    [...deploymentKeys.all(wsId), "operations", "detail", operationId] as const,
  logs: (wsId: string, operationId: string) =>
    [...deploymentKeys.all(wsId), "operations", operationId, "logs"] as const,
  sourceRevision: (wsId: string, projectId: string) =>
    [...deploymentKeys.all(wsId), "source-revision", projectId] as const,
};

export function deploymentConnectionsOptions(wsId: string) {
  return queryOptions<DeploymentProviderConnection[]>({
    queryKey: deploymentKeys.connections(wsId),
    queryFn: () => api.listDeploymentConnections(wsId),
    enabled: !!wsId,
  });
}

export function discoveredVercelProjectsOptions(
  wsId: string,
  connectionId: string,
) {
  return queryOptions<DiscoveredVercelProject[]>({
    queryKey: deploymentKeys.discoveredProjects(wsId, connectionId),
    queryFn: () => api.listDiscoveredVercelProjects(wsId, connectionId),
    enabled: !!wsId && !!connectionId,
  });
}

export function deploymentTargetsOptions(wsId: string, projectId?: string) {
  return queryOptions<DeploymentTarget[]>({
    queryKey: deploymentKeys.targets(wsId, projectId),
    queryFn: () => api.listDeploymentTargets(wsId, projectId),
    enabled: !!wsId,
  });
}

export function deploymentOperationsOptions(wsId: string, targetId: string) {
  return queryOptions<DeploymentOperation[]>({
    queryKey: deploymentKeys.operations(wsId, targetId),
    queryFn: () => api.listDeploymentOperations(wsId, targetId),
    enabled: !!wsId && !!targetId,
  });
}

export function deploymentSourceRevisionOptions(wsId: string, projectId: string) {
  return queryOptions<DeploymentSourceRevision>({
    queryKey: deploymentKeys.sourceRevision(wsId, projectId),
    queryFn: () => api.getDeploymentSourceRevision(projectId),
    enabled: !!wsId && !!projectId,
  });
}

export function deploymentOperationDetailOptions(
  wsId: string,
  operationId: string,
) {
  return queryOptions<DeploymentOperation>({
    queryKey: deploymentKeys.operation(wsId, operationId),
    queryFn: () => api.getDeploymentOperation(wsId, operationId),
    enabled: !!wsId && !!operationId,
  });
}

export function deploymentLogsOptions(wsId: string, operationId: string) {
  return queryOptions<DeploymentLogResult>({
    queryKey: deploymentKeys.logs(wsId, operationId),
    queryFn: () => api.getDeploymentLogs(wsId, operationId),
    enabled: !!wsId && !!operationId,
  });
}
