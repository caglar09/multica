import type { AutonomousRoleRuntimeAssignment } from "../types";
import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";

export const autonomousProjectKeys = {
  all: (wsId: string) => ["autonomous-projects", wsId] as const,
  detail: (wsId: string, projectId: string) =>
    [...autonomousProjectKeys.all(wsId), "detail", projectId] as const,
};

export function autonomousProjectOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: autonomousProjectKeys.detail(wsId, projectId),
    queryFn: () => api.getProjectAutonomous(projectId),
    refetchInterval: 5000,
    staleTime: 1500,
  });
}

function useAutonomousControlMutation<TData>(
  mutationFn: (projectId: string) => Promise<TData>,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn,
    onSettled: (_data, _err, projectId) => {
      qc.invalidateQueries({
        queryKey: autonomousProjectKeys.detail(wsId, projectId),
      });
    },
  });
}

export function usePauseAutonomousProject() {
  return useAutonomousControlMutation((projectId) =>
    api.pauseProjectAutonomous(projectId),
  );
}

export function useResumeAutonomousProject() {
  return useAutonomousControlMutation((projectId) =>
    api.resumeProjectAutonomous(projectId),
  );
}

export function useReplanAutonomousProject() {
  return useAutonomousControlMutation((projectId) =>
    api.replanProjectAutonomous(projectId),
  );
}

export function useRetryAutonomousAction() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: ({
      projectId,
      actionId,
    }: {
      projectId: string;
      actionId: string;
    }) => api.retryProjectAutonomousAction(projectId, actionId),
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({
        queryKey: autonomousProjectKeys.detail(wsId, vars.projectId),
      });
    },
  });
}


export function useConfirmAutonomousTeam() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

  return useMutation({
    mutationFn: ({
      projectId,
      assignments,
    }: {
      projectId: string;
      assignments: AutonomousRoleRuntimeAssignment[];
    }) => api.confirmProjectAutonomousTeam(projectId, assignments),
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({
        queryKey: autonomousProjectKeys.detail(wsId, vars.projectId),
      });
    },
  });
}
