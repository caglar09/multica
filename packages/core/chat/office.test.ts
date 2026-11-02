import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { sendOfficeChatMessage } from "./office";

vi.mock("../api", () => ({
  api: {
    createChatSession: vi.fn(),
    sendChatMessage: vi.fn(),
  },
}));

describe("sendOfficeChatMessage", () => {
  beforeEach(() => vi.resetAllMocks());

  it("creates a session for the selected agent and project before sending", async () => {
    vi.mocked(api.createChatSession).mockResolvedValue({ id: "session-new" } as never);
    vi.mocked(api.sendChatMessage).mockResolvedValue({ message_id: "message-1" } as never);
    const onSessionCreated = vi.fn(() => {
      expect(api.sendChatMessage).not.toHaveBeenCalled();
    });

    const result = await sendOfficeChatMessage({
      agentId: "agent-2",
      projectId: "project-3",
      title: "Inspect this issue",
      content: "Inspect this issue",
      clientMessageId: "client-message-1",
      onSessionCreated,
    });

    expect(api.createChatSession).toHaveBeenCalledWith({
      agent_id: "agent-2",
      project_id: "project-3",
      title: "Inspect this issue",
    });
    expect(onSessionCreated).toHaveBeenCalledWith("session-new");
    expect(api.sendChatMessage).toHaveBeenCalledWith(
      "session-new",
      "Inspect this issue",
      undefined,
      "client-message-1",
    );
    expect(result.sessionId).toBe("session-new");
  });

  it("reuses an existing session without creating another", async () => {
    vi.mocked(api.sendChatMessage).mockResolvedValue({ message_id: "message-2" } as never);

    await sendOfficeChatMessage({
      agentId: "agent-2",
      projectId: "project-3",
      sessionId: "session-existing",
      content: "Continue",
      clientMessageId: "client-message-2",
    });

    expect(api.createChatSession).not.toHaveBeenCalled();
    expect(api.sendChatMessage).toHaveBeenCalledWith(
      "session-existing",
      "Continue",
      undefined,
      "client-message-2",
    );
  });

  it("does not create an empty session", async () => {
    await expect(
      sendOfficeChatMessage({
        agentId: "agent-2",
        projectId: null,
        content: " \n ",
        clientMessageId: "client-message-3",
      }),
    ).rejects.toThrow("Chat message cannot be empty");

    expect(api.createChatSession).not.toHaveBeenCalled();
    expect(api.sendChatMessage).not.toHaveBeenCalled();
  });
});
