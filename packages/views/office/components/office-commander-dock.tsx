"use client";

import { useCallback, useEffect, useState } from "react";
import { useCreateIssue } from "@multica/core/issues/mutations";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import {
  Bot,
  Check,
  CornerDownLeft,
  ExternalLink,
  Loader2,
  MessageSquare,
  RotateCcw,
  Send,
  Square,
} from "lucide-react";
import { toast } from "sonner";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { ChatMessageList } from "../../chat/components/chat-message-list";
import { useChatTaskActions } from "../../chat/components/use-chat-task-actions";
import type { OfficeAgentDesk } from "../types";
import type { useOfficeConversation } from "../use-office-conversation";

type Conversation = ReturnType<typeof useOfficeConversation>;
type DockMode = "ask" | "assign";
type DockStatusKey =
  | "loading"
  | "queued"
  | "busy"
  | "offline"
  | "forbidden"
  | "archived"
  | "runtime_unavailable"
  | "unavailable"
  | "leader_unavailable"
  | "error";

interface OfficeCommanderDockProps {
  projectId: string | null;
  desks: OfficeAgentDesk[];
  selectedDesk: OfficeAgentDesk | null;
  onSelectDesk: (desk: OfficeAgentDesk | null) => void;
  conversation: Conversation;
  className?: string;
}

const QUICK_PROMPTS = ["status", "completed", "blockers", "gates"] as const;

function statusKey(status: Conversation["status"], isLeader: boolean): DockStatusKey | null {
  switch (status) {
    case "loading":
      return "loading";
    case "queued":
      return "queued";
    case "busy":
      return "busy";
    case "offline":
      return "offline";
    case "forbidden":
      return "forbidden";
    case "archived":
      return "archived";
    case "runtime_unavailable":
      return "runtime_unavailable";
    case "unavailable":
      return isLeader ? "leader_unavailable" : "unavailable";
    case "error":
      return "error";
    default:
      return null;
  }
}

export function OfficeCommanderDock({
  projectId,
  desks,
  selectedDesk,
  onSelectDesk,
  conversation,
  className,
}: OfficeCommanderDockProps) {
  const { t } = useT("projects");
  const { t: tChat } = useT("chat");
  const paths = useWorkspacePaths();
  const createIssue = useCreateIssue();
  const [mode, setMode] = useState<DockMode>("ask");
  const [input, setInput] = useState("");
  const [isPanelOpen, setIsPanelOpen] = useState(false);
  const [assignmentError, setAssignmentError] = useState(false);
  const [createdIssue, setCreatedIssue] = useState<{ identifier: string } | null>(null);
  const [isCancelling, setIsCancelling] = useState(false);

  const targetDesk = selectedDesk ?? desks.find((desk) => desk.isLeader) ?? desks[0] ?? null;
  const isLeader = targetDesk?.isLeader === true;
  const displayedStatusKey = statusKey(conversation.status, isLeader);
  const restoreCancelledMessage = useCallback(
    (message: { content: string }) => setInput(message.content),
    [],
  );
  const { cancelChatTask } = useChatTaskActions(conversation.sessionId, restoreCancelledMessage);

  useEffect(() => {
    if (conversation.sessionId) setIsPanelOpen(true);
  }, [conversation.sessionId]);

  const handleSend = async (message = input, clearInput = true) => {
    const content = message.trim();
    if (!content || !conversation.canSend) return;
    setIsPanelOpen(true);
    if (await conversation.send(content) && clearInput) setInput("");
  };

  const handleRetry = async () => {
    if (await conversation.retry()) setInput("");
  };

  const handleStop = async () => {
    const pending = conversation.pending;
    const sessionId = conversation.sessionId;
    if (!pending?.task_id || !sessionId || isCancelling) return;
    setIsCancelling(true);
    try {
      const result = await cancelChatTask(pending.task_id, sessionId, {
        restoreDraftToInput: true,
        source: "office-dock-stop",
      });
      if (!result) toast.error(tChat(($) => $.queue.action_failed_toast));
    } finally {
      setIsCancelling(false);
    }
  };

  const handleAssign = async () => {
    const title = input.trim();
    if (!title || !projectId || !targetDesk?.agentId || createIssue.isPending) return;
    setAssignmentError(false);
    setCreatedIssue(null);
    try {
      const issue = await createIssue.mutateAsync({
        title,
        assignee_type: "agent",
        assignee_id: targetDesk.agentId,
        project_id: projectId,
      });
      setInput("");
      setCreatedIssue({ identifier: issue.identifier });
    } catch {
      setAssignmentError(true);
    }
  };

  const handleSubmit = () => {
    if (mode === "ask") void handleSend();
    else void handleAssign();
  };

  return (
    <section
      aria-label={t(($) => $.office.dock.title)}
      className={cn("pointer-events-auto mx-auto flex w-full max-w-4xl flex-col gap-2 px-3", className)}
    >
      {isPanelOpen && mode === "ask" ? (
        <div className="overflow-hidden rounded-xl border border-border bg-card/95 shadow-xl backdrop-blur-xl">
          <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
            <div className="flex min-w-0 items-center gap-2">
              <Bot aria-hidden="true" className="size-4 shrink-0 text-primary" />
              <h2 className="truncate text-label font-semibold text-foreground">
                {targetDesk?.agentName ?? t(($) => $.office.dock.title)}
              </h2>
              {targetDesk?.role ? (
                <span className="truncate text-caption text-muted-foreground">{targetDesk.role}</span>
              ) : null}
            </div>
            <div className="flex shrink-0 items-center gap-1">
              {conversation.pending?.task_id && conversation.sessionId ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={isCancelling}
                  onClick={() => void handleStop()}
                  aria-label={tChat(($) => $.input.stop_tooltip)}
                >
                  {isCancelling ? (
                    <Loader2 aria-hidden="true" className="size-3.5 animate-spin motion-reduce:animate-none" />
                  ) : (
                    <Square aria-hidden="true" className="size-3" />
                  )}
                  <span>{tChat(($) => $.input.stop_tooltip)}</span>
                </Button>
              ) : null}
              {conversation.sessionId ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => conversation.openInChat()}
                  aria-label={t(($) => $.office.dock.open_chat)}
                >
                  <MessageSquare aria-hidden="true" className="size-3.5" />
                  <span>{t(($) => $.office.dock.open_chat)}</span>
                  <ExternalLink aria-hidden="true" className="size-3.5" />
                </Button>
              ) : null}
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                aria-label={t(($) => $.office.dock.close)}
                onClick={() => setIsPanelOpen(false)}
              >
                <span aria-hidden="true">×</span>
              </Button>
            </div>
          </div>

          <div className="flex h-64 min-h-0 flex-col p-2">
            {conversation.messages.length > 0 || conversation.pending?.task_id ? (
              <ChatMessageList
                active={isPanelOpen && mode === "ask"}
                messages={conversation.messages}
                pendingTask={conversation.pending}
                availability={conversation.availability}
              />
            ) : (
              <p className="py-4 text-center text-body text-muted-foreground">
                {t(($) => $.office.dock.empty)}
              </p>
            )}
            {conversation.sendError ? (
              <div className="flex items-center justify-between gap-3 rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-body text-destructive">
                <p>{t(($) => $.office.dock.error)}</p>
                {conversation.sendError.kind === "failed" ? (
                  <Button type="button" size="sm" variant="outline" onClick={() => void handleRetry()}>
                    <RotateCcw aria-hidden="true" className="size-3.5" />
                    {t(($) => $.office.dock.retry)}
                  </Button>
                ) : null}
              </div>
            ) : null}
          </div>
        </div>
      ) : null}

      <div className="rounded-xl border border-border bg-card/95 p-2 shadow-xl backdrop-blur-xl">
        <div className="mb-2 flex items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant={mode === "ask" ? "secondary" : "ghost"}
            aria-pressed={mode === "ask"}
            onClick={() => {
              setMode("ask");
              setAssignmentError(false);
              setCreatedIssue(null);
            }}
          >
            {t(($) => $.office.dock.ask_mode)}
          </Button>
          <Button
            type="button"
            size="sm"
            variant={mode === "assign" ? "secondary" : "ghost"}
            aria-pressed={mode === "assign"}
            onClick={() => {
              setMode("assign");
              setIsPanelOpen(false);
            }}
          >
            {t(($) => $.office.dock.assign_mode)}
          </Button>

          <label htmlFor="office-target" className="sr-only">
            {t(($) => $.office.dock.agent_label)}
          </label>
          <select
            id="office-target"
            value={targetDesk?.id ?? ""}
            onChange={(event) => onSelectDesk(desks.find((desk) => desk.id === event.target.value) ?? null)}
            className="ml-auto max-w-[45%] rounded-md border border-input bg-background px-2 py-1.5 text-label text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={t(($) => $.office.dock.agent_label)}
          >
            {desks.length === 0 ? <option value="">{t(($) => $.office.dock.target_required)}</option> : null}
            {desks.map((desk) => (
              <option key={desk.id} value={desk.id}>
                {desk.agentName}
              </option>
            ))}
          </select>
        </div>

        <form
          onSubmit={(event) => {
            event.preventDefault();
            handleSubmit();
          }}
          className="flex items-center gap-2"
        >
          <label htmlFor="office-message" className="sr-only">
            {mode === "ask"
              ? t(($) => $.office.dock.message_label)
              : t(($) => $.office.dock.task_title_label)}
          </label>
          <input
            id="office-message"
            type="text"
            value={input}
            onChange={(event) => {
              setInput(event.target.value);
              setAssignmentError(false);
            }}
            onFocus={() => {
              if (mode === "ask") setIsPanelOpen(true);
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                handleSubmit();
              }
            }}
            placeholder={t(($) => mode === "ask"
              ? $.office.dock.message_placeholder
              : $.office.dock.task_title_placeholder)}
            disabled={mode === "ask"
              ? !conversation.canSend
              : !projectId || !targetDesk?.agentId || createIssue.isPending}
            className="min-w-0 flex-1 bg-transparent px-2 py-2 text-body text-foreground outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-60"
          />
          <Button
            type="submit"
            size="sm"
            disabled={mode === "ask"
              ? !input.trim() || !conversation.canSend
              : !input.trim() || !projectId || !targetDesk?.agentId || createIssue.isPending}
            aria-label={mode === "ask"
              ? t(($) => $.office.dock.send)
              : t(($) => $.office.dock.assign_mode)}
          >
            {(conversation.isSending && mode === "ask") || createIssue.isPending ? (
              <Loader2 aria-hidden="true" className="size-4 animate-spin motion-reduce:animate-none" />
            ) : mode === "ask" ? (
              <Send aria-hidden="true" className="size-4" />
            ) : (
              <Check aria-hidden="true" className="size-4" />
            )}
            <span>{mode === "ask" ? t(($) => $.office.dock.send) : t(($) => $.office.dock.assign_mode)}</span>
            {mode === "ask" ? <CornerDownLeft aria-hidden="true" className="size-3.5" /> : null}
          </Button>
        </form>

        {mode === "ask" && displayedStatusKey ? (
          <p className="mt-2 text-caption text-muted-foreground" role="status" aria-live="polite">
            {t(($) => $.office.dock[displayedStatusKey])}
          </p>
        ) : null}
        {mode === "assign" && !projectId ? (
          <p className="mt-2 text-caption text-muted-foreground" role="status">
            {t(($) => $.office.dock.project_required)}
          </p>
        ) : null}
        {mode === "assign" && projectId && !targetDesk?.agentId ? (
          <p className="mt-2 text-caption text-muted-foreground" role="status">
            {t(($) => $.office.dock.target_required)}
          </p>
        ) : null}
        {assignmentError ? (
          <p className="mt-2 text-caption text-destructive" role="alert">
            {t(($) => $.office.dock.error)}
          </p>
        ) : null}
        {createdIssue ? (
          <p className="mt-2 flex items-center gap-2 text-body text-foreground" role="status">
            <span>{t(($) => $.office.dock.issue_created, { identifier: createdIssue.identifier })}</span>
            <AppLink className="inline-flex items-center gap-1 text-primary underline-offset-4 hover:underline" href={paths.issueDetail(createdIssue.identifier)}>
              {t(($) => $.office.dock.open_issue)}
              <ExternalLink aria-hidden="true" className="size-3.5" />
            </AppLink>
          </p>
        ) : null}
      </div>

      {mode === "ask" ? (
        <div className="flex flex-wrap items-center justify-center gap-1.5">
          {QUICK_PROMPTS.map((kind) => (
            <Button
              key={kind}
              type="button"
              size="sm"
              variant="outline"
              className="h-7 rounded-full px-2.5 text-caption"
              disabled={!projectId || !conversation.canSend}
              onClick={() => void handleSend(t(($) => $.office.dock[`prompt_${kind}`]), false)}
            >
              {t(($) => $.office.dock[`prompt_${kind}`])}
            </Button>
          ))}
        </div>
      ) : null}
    </section>
  );
}
