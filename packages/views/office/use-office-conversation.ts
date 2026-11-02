"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { dispatchReasonCode } from "@multica/core/api";
import {
  isAgentRuntimeBound,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { useChatStore } from "@multica/core/chat";
import {
  chatKeys,
  chatMessagesOptions,
  chatSessionsOptions,
  pendingChatTaskOptions,
  useTaskMessages,
} from "@multica/core/chat/queries";
import { enqueuePendingChatTask, hideQueuedChatMessages } from "@multica/core/chat/pending";
import { upsertChatMessageToCaches } from "@multica/core/chat/message-cache";
import { sendOfficeChatMessage } from "@multica/core/chat/office";
import type { Agent, ChatPendingTask, ChatSession } from "@multica/core/types";
import type { AgentAvailability } from "@multica/core/agents";
import type { OfficeAgentDesk } from "./types";

export type OfficeConversationStatus =
  | "loading"
  | "ready"
  | "queued"
  | "busy"
  | "offline"
  | "forbidden"
  | "archived"
  | "runtime_unavailable"
  | "unavailable"
  | "error";

export interface OfficeConversationError {
  kind: "forbidden" | "runtime_unavailable" | "failed";
  reasonCode?: string;
}

export interface UseOfficeConversationOptions {
  projectId: string | null;
  desk: OfficeAgentDesk | null;
  leaderSessionId?: string | null;
  leaderAgentId?: string | null;
}

interface SendAttempt {
  targetKey: string;
  content: string;
  clientMessageId: string;
  sessionId: string | null;
}

function failedSend(error: unknown): OfficeConversationError {
  const reasonCode = dispatchReasonCode(error);
  if (reasonCode === "invocation_not_allowed") {
    return { kind: "forbidden", reasonCode };
  }
  if (
    reasonCode === "agent_runtime_required" ||
    reasonCode === "runtime_access_denied"
  ) {
    return { kind: "runtime_unavailable", reasonCode };
  }
  return { kind: "failed", ...(reasonCode ? { reasonCode } : {}) };
}

function sessionForDesk(
  sessions: ChatSession[],
  agentId: string,
  projectId: string | null,
) {
  return sessions
    .filter(
      (session) =>
        session.agent_id === agentId &&
        (session.project_id ?? null) === projectId &&
        session.status !== "archived",
    )
    .sort(
      (left, right) =>
        Date.parse(right.last_message?.created_at ?? right.updated_at) -
        Date.parse(left.last_message?.created_at ?? left.updated_at),
    )[0];
}

function officeTitle(content: string): string {
  const firstLine = content.split("\n").find((line) => line.trim())?.trim() ?? "";
  return firstLine.length > 30 ? `${firstLine.slice(0, 29).trimEnd()}…` : firstLine;
}

/** Server-backed, persistent chat for one desk. */
export function useOfficeConversation({
  projectId,
  desk,
  leaderSessionId,
  leaderAgentId,
}: UseOfficeConversationOptions) {
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const userId = useAuthStore((state) => state.user?.id ?? null);
  const isLeader = desk?.isLeader === true;
  const agentId = isLeader
    ? leaderAgentId ?? desk?.agentId ?? desk?.rawAgent?.id
    : desk?.agentId ?? desk?.rawAgent?.id;
  const targetKey = `${isLeader ? "leader" : "agent"}:${agentId ?? ""}:${projectId ?? ""}`;
  const leaderSessionPending = isLeader && !!projectId && leaderSessionId === undefined;

  const agentQuery = useQuery({
    ...agentListOptions(wsId),
    enabled: !!agentId,
  });
  const memberQuery = useQuery(memberListOptions(wsId));
  const sessionsQuery = useQuery({
    ...chatSessionsOptions(wsId),
  });

  const agent: Agent | undefined =
    agentQuery.data?.find((item) => item.id === agentId) ??
    (desk?.rawAgent?.id === agentId ? desk?.rawAgent : undefined);
  const currentMember = memberQuery.data?.find((member) => member.user_id === userId);
  const permission = agent
    ? canAssignAgentToIssue(agent, {
        userId,
        role: currentMember?.role ?? null,
      })
    : null;
  const presence = useAgentPresenceDetail(wsId, agentId);
  const availability: AgentAvailability | undefined =
    presence === "loading" ? undefined : presence.availability;

  const matchingSession =
    !leaderSessionPending && agentId && sessionsQuery.data
      ? sessionForDesk(
          sessionsQuery.data.filter((session) => session.id !== leaderSessionId),
          agentId,
          projectId,
        )
      : undefined;
  const [createdSession, setCreatedSession] = useState<{
    targetKey: string;
    sessionId: string;
  } | null>(null);
  const createdSessionId =
    createdSession?.targetKey === targetKey ? createdSession.sessionId : null;
  const sessionId = createdSessionId ?? matchingSession?.id ?? null;
  const sessionRecord = sessionId
    ? sessionsQuery.data?.find((session) => session.id === sessionId)
    : undefined;

  const messagesQuery = useQuery({
    ...chatMessagesOptions(sessionId ?? ""),
    enabled: !!sessionId,
  });
  const pendingQuery = useQuery({
    ...pendingChatTaskOptions(sessionId ?? ""),
    enabled: !!sessionId,
  });
  const pending: ChatPendingTask | null = pendingQuery.data ?? null;
  const messages = useMemo(
    () => hideQueuedChatMessages(messagesQuery.data ?? [], pending ?? undefined),
    [messagesQuery.data, pending],
  );
  const taskId = pending?.task_id ?? "";
  const taskIsLive = Boolean(
    taskId &&
      pending?.status !== "queued" &&
      pending?.status !== "completed" &&
      pending?.status !== "failed" &&
      pending?.status !== "cancelled",
  );
  const taskMessagesQuery = useTaskMessages(taskId, taskIsLive, !!sessionId && !!taskId);

  const [sendError, setSendError] = useState<OfficeConversationError | null>(null);
  const [isSending, setIsSending] = useState(false);
  const isSendingRef = useRef(false);
  const attemptRef = useRef<SendAttempt | null>(null);

  useEffect(() => {
    attemptRef.current = null;
    setSendError(null);
  }, [targetKey]);

  const queryIsLoading =
    memberQuery.isPending ||
    (agentId ? agentQuery.isPending : false) ||
    presence === "loading" ||
    sessionsQuery.isPending ||
    leaderSessionPending;
  const queryHasError =
    memberQuery.isError ||
    (agentId ? agentQuery.isError : false) ||
    sessionsQuery.isError;

  let baseStatus: OfficeConversationStatus = "ready";
  if (!desk || !agentId) baseStatus = "unavailable";
  else if (queryIsLoading) baseStatus = "loading";
  else if (queryHasError) baseStatus = "error";
  else if (agent?.archived_at || availability === "archived" || sessionRecord?.status === "archived") {
    baseStatus = "archived";
  } else if (!isLeader && permission && !permission.allowed) baseStatus = "forbidden";
  else if (!agent || !permission || !currentMember) baseStatus = "loading";
  else if (!isAgentRuntimeBound(agent)) baseStatus = "runtime_unavailable";
  else if (taskIsLive && pending?.supports_queue !== true) baseStatus = "busy";
  else if (pending?.status === "queued" || (pending?.queued_tasks?.length ?? 0) > 0) {
    baseStatus = "queued";
  } else if (availability !== "online") baseStatus = "offline";

  const status =
    sendError?.kind === "failed"
      ? "error"
      : sendError?.kind === "forbidden"
        ? "forbidden"
        : sendError?.kind === "runtime_unavailable"
          ? "runtime_unavailable"
          : baseStatus;
  const canSend =
    !isSending &&
    (baseStatus === "ready" || baseStatus === "queued" || baseStatus === "offline") &&
    sendError?.kind !== "forbidden" &&
    sendError?.kind !== "runtime_unavailable";

  const send = useCallback(
    async (content: string, retrying = false): Promise<boolean> => {
      const trimmed = content.trim();
      if (!trimmed || !agentId || !agent || (!isLeader && !permission?.allowed)) return false;
      if (isSendingRef.current) return false;
      if (
        baseStatus === "loading" ||
        baseStatus === "unavailable" ||
        baseStatus === "archived" ||
        baseStatus === "forbidden" ||
        baseStatus === "runtime_unavailable" ||
        baseStatus === "error" ||
        (baseStatus === "busy" && !retrying)
      ) {
        return false;
      }
      const previous = attemptRef.current;
      const attempt =
        previous?.targetKey === targetKey && previous.content === content
          ? previous
          : {
              targetKey,
              content,
              clientMessageId: crypto.randomUUID(),
              sessionId,
            };
      attemptRef.current = attempt;
      isSendingRef.current = true;
      setIsSending(true);
      setSendError(null);

      try {
        const result = await sendOfficeChatMessage({
          agentId,
          projectId,
          sessionId: attempt.sessionId ?? sessionId,
          title: officeTitle(content),
          content,
          clientMessageId: attempt.clientMessageId,
          onSessionCreated: (createdId) => {
            attempt.sessionId = createdId;
            setCreatedSession({ targetKey, sessionId: createdId });
            queryClient.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
          },
        });
        attempt.sessionId = result.sessionId;
        upsertChatMessageToCaches(
          queryClient,
          result.sessionId,
          {
            id: result.response.message_id,
            chat_session_id: result.sessionId,
            role: "user",
            content,
            task_id: result.response.task_id,
            created_at: result.response.created_at,
          },
          { seedIfMissing: true },
        );
        queryClient.setQueryData<ChatPendingTask>(
          chatKeys.pendingTask(result.sessionId),
          (old) => {
            const next = enqueuePendingChatTask(
              old,
              {
                task_id: result.response.task_id,
                status: "queued",
                created_at: result.response.created_at,
                message_id: result.response.message_id,
                content,
              },
              result.response.queued,
            );
            if (result.response.supports_queue === true || old?.supports_queue === true) {
              next.supports_queue = true;
            }
            return next;
          },
        );
        await queryClient.invalidateQueries({
          queryKey: chatKeys.pendingTask(result.sessionId),
        });
        await queryClient.invalidateQueries({
          queryKey: chatKeys.messages(result.sessionId),
        });
        attemptRef.current = null;
        return true;
      } catch (error) {
        setSendError(failedSend(error));
        return false;
      } finally {
        isSendingRef.current = false;
        setIsSending(false);
      }
    },
    [
      agent,
      agentId,
      baseStatus,
      isLeader,
      permission?.allowed,
      projectId,
      queryClient,
      sessionId,
      targetKey,
      wsId,
    ],
  );

  const retry = useCallback((): Promise<boolean> => {
    const attempt = attemptRef.current;
    if (!attempt || attempt.targetKey !== targetKey || sendError?.kind !== "failed") {
      return Promise.resolve(false);
    }
    return send(attempt.content, true);
  }, [send, sendError, targetKey]);

  const openInChat = useCallback((): boolean => {
    if (!sessionId || !agentId) return false;
    const chatStore = useChatStore.getState();
    chatStore.setActiveSession(sessionId);
    chatStore.setSelectedAgentId(agentId);
    chatStore.setSelectedProjectId(projectId);
    chatStore.setOpen(true);
    return true;
  }, [agentId, projectId, sessionId]);

  return {
    agentId,
    sessionId,
    messages,
    pending,
    taskMessages: taskMessagesQuery.data ?? [],
    availability,
    status,
    isLoading: queryIsLoading || (!!sessionId && (messagesQuery.isPending || pendingQuery.isPending)),
    isSending,
    canSend,
    sendError,
    send,
    retry,
    openInChat,
  };
}
