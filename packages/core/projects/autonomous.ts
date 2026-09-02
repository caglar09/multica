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

function useAutonomousControlMutation(
  action: "pause" | "resume" | "replan",
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (projectId: string) => {
      switch (action) {
        case "pause":
          return api.pauseProjectAutonomous(projectId);
        case "resume":
          return api.resumeProjectAutonomous(projectId);
        case "replan":
          return api.replanProjectAutonomous(projectId);
      }
    },
    onSettled: (_data, _err, projectId) => {
      qc.invalidateQueries({
        queryKey: autonomousProjectKeys.detail(wsId, projectId),
      });
    },
  });
}

export function usePauseAutonomousProject() {
  return useAutonomousControlMutation("pause");
}

export function useResumeAutonomousProject() {
  return useAutonomousControlMutation("resume");
}

export function useReplanAutonomousProject() {
  return useAutonomousControlMutation("replan");
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
