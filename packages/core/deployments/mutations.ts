import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { deploymentKeys } from "./queries";
import type {
  CreateDeploymentTargetInput,
  TriggerDeploymentInput,
  PromoteDeploymentInput,
  RollbackDeploymentInput,
  CreateDeploymentConnectionInput,
  DeploymentTarget,
  DeploymentOperation,
} from "../types/deployment";

export function useCreateDeploymentTarget() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: CreateDeploymentTargetInput) =>
      api.createDeploymentTarget(wsId, data),
    onSuccess: (newTarget) => {
      qc.setQueryData<DeploymentTarget[]>(
        deploymentKeys.targets(wsId, newTarget.project_id),
        (old = []) => [...old, newTarget],
      );
      qc.invalidateQueries({ queryKey: deploymentKeys.targets(wsId) });
    },
  });
}

export function useCreateDeploymentProviderTarget() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: CreateDeploymentTargetInput) =>
      api.createDeploymentProviderTarget(wsId, data.connection_id, data),
    onSuccess: (newTarget) => {
      qc.setQueryData<DeploymentTarget[]>(
        deploymentKeys.targets(wsId, newTarget.project_id),
        (old = []) => [...old, newTarget],
      );
      qc.invalidateQueries({ queryKey: deploymentKeys.targets(wsId) });
    },
  });
}

export function useUpdateDeploymentTarget() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: ({
      id,
      ...data
    }: { id: string } & Partial<CreateDeploymentTargetInput>) =>
      api.updateDeploymentTarget(wsId, id, data),
    onSuccess: (updated) => {
      qc.setQueryData<DeploymentTarget>(
        deploymentKeys.target(wsId, updated.id),
        updated,
      );
      qc.invalidateQueries({ queryKey: deploymentKeys.targets(wsId) });
    },
  });
}

export function useDeleteDeploymentTarget() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (targetId: string) =>
      api.deleteDeploymentTarget(wsId, targetId),
    onSuccess: (_data, targetId) => {
      qc.invalidateQueries({ queryKey: deploymentKeys.targets(wsId) });
      qc.removeQueries({ queryKey: deploymentKeys.target(wsId, targetId) });
    },
  });
}

export function useTriggerDeployment() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: TriggerDeploymentInput) =>
      api.triggerDeployment(wsId, data.target_id, data),
    onSuccess: (newOperation) => {
      qc.setQueryData<DeploymentOperation[]>(
        deploymentKeys.operations(wsId, newOperation.target_id),
        (old = []) => [newOperation, ...old],
      );
      qc.invalidateQueries({
        queryKey: deploymentKeys.operations(wsId, newOperation.target_id),
      });
    },
  });
}

export function useCancelDeployment() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (operationId: string) =>
      api.cancelDeployment(wsId, operationId),
    onSuccess: (updated) => {
      qc.setQueryData<DeploymentOperation>(
        deploymentKeys.operation(wsId, updated.id),
        updated,
      );
      qc.invalidateQueries({
        queryKey: deploymentKeys.operations(wsId, updated.target_id),
      });
    },
  });
}

export function usePromoteDeployment() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: PromoteDeploymentInput) =>
      api.promoteDeployment(wsId, data.target_id, data),
    onSuccess: (newOperation) => {
      qc.setQueryData<DeploymentOperation[]>(
        deploymentKeys.operations(wsId, newOperation.target_id),
        (old = []) => [newOperation, ...old],
      );
      qc.invalidateQueries({
        queryKey: deploymentKeys.operations(wsId, newOperation.target_id),
      });
    },
  });
}

export function useRollbackDeployment() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: RollbackDeploymentInput) =>
      api.rollbackDeployment(wsId, data.target_id, data),
    onSuccess: (newOperation) => {
      qc.setQueryData<DeploymentOperation[]>(
        deploymentKeys.operations(wsId, newOperation.target_id),
        (old = []) => [newOperation, ...old],
      );
      qc.invalidateQueries({
        queryKey: deploymentKeys.operations(wsId, newOperation.target_id),
      });
    },
  });
}

export function useDisconnectDeploymentConnection() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (connectionId: string) =>
      api.disconnectDeploymentConnection(wsId, connectionId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: deploymentKeys.connections(wsId) });
      qc.invalidateQueries({ queryKey: deploymentKeys.targets(wsId) });
    },
  });
}

export function useCreateDeploymentConnection() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: (data: CreateDeploymentConnectionInput) =>
      api.createDeploymentConnection(wsId, data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: deploymentKeys.connections(wsId) });
      qc.invalidateQueries({ queryKey: deploymentKeys.targets(wsId) });
    },
  });
}

export function useSetDeploymentWebhookSecret() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: ({ connectionId, webhookSecret }: { connectionId: string; webhookSecret: string }) =>
      api.setDeploymentWebhookSecret(wsId, connectionId, webhookSecret),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: deploymentKeys.connections(wsId) });
      qc.invalidateQueries({ queryKey: deploymentKeys.targets(wsId) });
    },
  });
}
