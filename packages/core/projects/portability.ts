import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { issueKeys } from "../issues/queries";
import type { ImportProjectRequest, SyncProjectRequest } from "../types";
import { autonomousProjectKeys } from "./autonomous";
import { projectKeys } from "./queries";
import { projectReportKeys } from "./report";

function invalidatePortabilityQueries(
  queryClient: QueryClient,
  wsId: string,
  projectId?: string,
) {
  queryClient.invalidateQueries({ queryKey: projectKeys.list(wsId) });
  queryClient.invalidateQueries({
    queryKey: projectId
      ? projectKeys.detail(wsId, projectId)
      : projectKeys.all(wsId),
  });
  queryClient.invalidateQueries({
    queryKey: projectId
      ? autonomousProjectKeys.detail(wsId, projectId)
      : autonomousProjectKeys.all(wsId),
  });
  queryClient.invalidateQueries({
    queryKey: projectId
      ? projectReportKeys.detail(wsId, projectId)
      : projectReportKeys.all(wsId),
  });
  queryClient.invalidateQueries({ queryKey: issueKeys.all(wsId) });
  queryClient.invalidateQueries({ queryKey: issueKeys.timelineAll() });
}

export function useExportProject(projectId: string) {
  const wsId = useWorkspaceId();
  return useQuery({
    queryKey: [...projectKeys.detail(wsId, projectId), "export"],
    queryFn: () => api.exportProject(projectId),
  });
}

export function useImportProject() {
  const queryClient = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: ImportProjectRequest) => api.importProject(data),
    onSettled: (result, _error, variables) => {
      if (!variables.dry_run) {
        invalidatePortabilityQueries(queryClient, wsId, result?.project_id ?? undefined);
      }
    },
  });
}

export function useSyncProject(projectId: string) {
  const queryClient = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: SyncProjectRequest) => api.syncProject(projectId, data),
    onSettled: (_result, _error, variables) => {
      if (!variables.dry_run) {
        invalidatePortabilityQueries(queryClient, wsId, projectId);
      }
    },
  });
}
