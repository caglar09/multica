"use client";

import { useMemo } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import {
  agentTaskSnapshotOptions,
  useWorkspacePresenceMap,
} from "@multica/core/agents";
import { isTaskMessageTaskId, taskMessagesOptions } from "@multica/core/chat/queries";
import { issueListOptions } from "@multica/core/issues";
import {
  autonomousProjectOptions,
  projectLeaderChangesOptions,
} from "@multica/core/projects";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions } from "@multica/core/workspace/queries";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import { buildVirtualOfficeScene } from "./spatial-mapper";

/** Combines workspace and project records into the live office scene. */
export function useOfficeLiveData(projectId?: string) {
  const wsId = useWorkspaceId();
  const agentsQuery = useQuery({ ...agentListOptions(wsId), enabled: Boolean(wsId) });
  const runtimesQuery = useQuery({ ...runtimeListOptions(wsId), enabled: Boolean(wsId) });
  const tasksQuery = useQuery({ ...agentTaskSnapshotOptions(wsId), enabled: Boolean(wsId) });
  const issuesQuery = useQuery({ ...issueListOptions(wsId), enabled: Boolean(wsId) });
  const presence = useWorkspacePresenceMap(wsId);
  const snapshotQuery = useQuery({
    ...autonomousProjectOptions(wsId, projectId ?? ""),
    enabled: Boolean(wsId && projectId),
  });
  const changesQuery = useQuery({
    ...projectLeaderChangesOptions(wsId, projectId ?? ""),
    enabled: Boolean(wsId && projectId),
  });

  const issues = useMemo(
    () => (issuesQuery.data ?? []).filter((issue) => !projectId || issue.project_id === projectId),
    [issuesQuery.data, projectId],
  );
  const knownProjectIssueIds = useMemo(() => {
    const ids = new Set(issues.map((issue) => issue.id));
    for (const workflow of snapshotQuery.data?.workflows ?? []) ids.add(workflow.issue_id);
    for (const node of snapshotQuery.data?.plan?.nodes ?? []) {
      if (node.materialized_issue_id) ids.add(node.materialized_issue_id);
    }
    return ids;
  }, [issues, snapshotQuery.data]);
  const tasks = useMemo(
    () => (tasksQuery.data ?? []).filter((task) => !projectId || Boolean(task.issue_id && knownProjectIssueIds.has(task.issue_id))),
    [knownProjectIssueIds, projectId, tasksQuery.data],
  );
  const visibleAgents = useMemo(() => {
    const allAgents = (agentsQuery.data ?? []).filter((agent) => !agent.archived_at);
    if (!projectId) return allAgents;

    const projectAgentIds = new Set<string>();
    const team = snapshotQuery.data?.team;
    if (team?.leader_agent_id) projectAgentIds.add(team.leader_agent_id);
    for (const member of team?.members ?? []) {
      if (member.active) projectAgentIds.add(member.agent_id);
    }
    for (const issue of issues) {
      if (issue.assignee_type === "agent" && issue.assignee_id) projectAgentIds.add(issue.assignee_id);
    }
    for (const task of tasks) projectAgentIds.add(task.agent_id);
    return allAgents.filter((agent) => projectAgentIds.has(agent.id));
  }, [agentsQuery.data, issues, projectId, snapshotQuery.data, tasks]);
  const activeRunIds = useMemo(
    () => [...new Set(tasks.filter((task) => task.status === "running").map((task) => task.id))]
      .filter(isTaskMessageTaskId),
    [tasks],
  );
  const hasUnqueryableActiveRun = tasks.some(
    (task) => task.status === "running" && !isTaskMessageTaskId(task.id),
  );
  const messageQueries = useQueries({
    queries: activeRunIds.map((taskId) => ({
      ...taskMessagesOptions(taskId),
      enabled: Boolean(wsId && isTaskMessageTaskId(taskId)),
    })),
  });
  const taskMessages = useMemo(
    () => messageQueries.flatMap((query) => query.data ?? []),
    [messageQueries],
  );
  const scene = useMemo(() => buildVirtualOfficeScene({
    snapshot: snapshotQuery.data,
    changeRequests: changesQuery.data?.items,
    agents: visibleAgents,
    presenceByAgent: presence.byAgent,
    tasks,
    issues,
    taskMessages,
  }), [
    changesQuery.data,
    issues,
    presence.byAgent,
    snapshotQuery.data,
    taskMessages,
    tasks,
    visibleAgents,
  ]);

  const scopedQueriesPending = [agentsQuery, tasksQuery, issuesQuery].some((query) => query.isPending && !query.isError);
  const projectQueriesPending = Boolean(projectId && [snapshotQuery, changesQuery].some((query) => query.isPending && !query.isError));
  const isLoading = !wsId || scopedQueriesPending || projectQueriesPending || presence.loading || messageQueries.some((query) => query.isPending && !query.isError);
  const isPartial = !wsId ||
    [agentsQuery, tasksQuery, issuesQuery].some((query) => query.isError || query.data === undefined) ||
    runtimesQuery.isError || runtimesQuery.data === undefined ||
    Boolean(projectId && (snapshotQuery.isError || snapshotQuery.data === undefined || changesQuery.isError || changesQuery.data === undefined)) ||
    presence.loading ||
    messageQueries.some((query) => query.isError || query.data === undefined) ||
    hasUnqueryableActiveRun ||
    Boolean(snapshotQuery.data?.partial);

  return { scene, isLoading, isPartial };
}
