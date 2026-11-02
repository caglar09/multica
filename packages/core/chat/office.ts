import { api } from "../api";
import type { SendChatMessageResponse } from "../types/chat";

export interface SendOfficeChatMessageInput {
  agentId: string;
  projectId: string | null;
  sessionId?: string | null;
  title?: string;
  content: string;
  clientMessageId: string;
  onSessionCreated?: (sessionId: string) => void;
}

export interface SendOfficeChatMessageResult {
  sessionId: string;
  response: SendChatMessageResponse;
}

/** Creates a direct agent session only when needed, then sends with stable identity. */
export async function sendOfficeChatMessage(
  input: SendOfficeChatMessageInput,
): Promise<SendOfficeChatMessageResult> {
  if (!input.content.trim()) throw new Error("Chat message cannot be empty");

  let sessionId = input.sessionId ?? null;
  if (!sessionId) {
    const session = await api.createChatSession({
      agent_id: input.agentId,
      project_id: input.projectId,
      ...(input.title ? { title: input.title } : {}),
    });
    sessionId = session.id;
    input.onSessionCreated?.(sessionId);
  }

  const response = await api.sendChatMessage(
    sessionId,
    input.content,
    undefined,
    input.clientMessageId,
  );
  return { sessionId, response };
}
