import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const projectReportKeys = {
  all: (wsId: string) => ["project-reports", wsId] as const,
  detail: (wsId: string, projectId: string) =>
    [...projectReportKeys.all(wsId), "detail", projectId] as const,
};

export function projectReportOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectReportKeys.detail(wsId, projectId),
    queryFn: () => api.getProjectReport(projectId),
    refetchInterval: 10_000,
    staleTime: 2_500,
  });
}
