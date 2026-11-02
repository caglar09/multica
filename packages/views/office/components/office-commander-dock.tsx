"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useCreateIssue } from "@multica/core/issues/mutations";
import type { ChatMessage } from "@multica/core/types";
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
  UserRound,
} from "lucide-react";
import { useLocale, useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
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

function messageDate(value: string, locale: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? ""
    : new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }).format(date);
}

function messageContent(message: ChatMessage, failedMessage: string): string {
  return message.failure_reason ? failedMessage : message.content;
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
  const locale = useLocale();
  const paths = useWorkspacePaths();
  const createIssue = useCreateIssue();
  const [mode, setMode] = useState<DockMode>("ask");
  const [input, setInput] = useState("");
  const [isPanelOpen, setIsPanelOpen] = useState(false);
  const [assignmentError, setAssignmentError] = useState(false);
  const [createdIssue, setCreatedIssue] = useState<{ identifier: string } | null>(null);
  const transcriptRef = useRef<HTMLDivElement>(null);

  const targetDesk = selectedDesk ?? desks.find((desk) => desk.isLeader) ?? desks[0] ?? null;
  const isLeader = targetDesk?.isLeader === true;
  const displayedStatusKey = statusKey(conversation.status, isLeader);
  const activeTaskMessages = useMemo(() => {
    const pendingStatus = conversation.pending?.status;
    if (!conversation.pending?.task_id || ["queued", "completed", "failed", "cancelled"].includes(pendingStatus ?? "")) {
      return [];
    }
    return conversation.taskMessages.filter(
      (message) => message.type === "text" || message.type === "thinking",
    );
  }, [conversation.pending, conversation.taskMessages]);

  useEffect(() => {
    const element = transcriptRef.current;
    if (element) element.scrollTop = element.scrollHeight;
  }, [conversation.messages, activeTaskMessages, conversation.pending, isPanelOpen]);

  const handleSend = async (message = input, clearInput = true) => {
    const content = message.trim();
    if (!content || !conversation.canSend) return;
    setIsPanelOpen(true);
    if (await conversation.send(content) && clearInput) setInput("");
  };

  const handleRetry = async () => {
    if (await conversation.retry()) setInput("");
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

          <div
            ref={transcriptRef}
            role="log"
            aria-live="polite"
            aria-relevant="additions text"
            className="max-h-72 space-y-3 overflow-y-auto p-4"
          >
            {conversation.messages.map((message) => (
              <article
                key={message.id}
                className={cn(
                  "max-w-[88%] rounded-xl border p-3 text-body",
                  message.role === "user"
                    ? "ml-auto border-primary/30 bg-primary/10 text-foreground"
                    : "mr-auto border-border bg-muted/40 text-foreground",
                )}
              >
                <div className="mb-1 flex items-center justify-between gap-3 text-caption text-muted-foreground">
                  <span className="flex items-center gap-1.5 font-medium">
                    {message.role === "user" ? (
                      <UserRound aria-hidden="true" className="size-3.5" />
                    ) : (
                      <Bot aria-hidden="true" className="size-3.5" />
                    )}
                    {message.role === "user"
                      ? t(($) => $.office.dock.you)
                      : targetDesk?.agentName ?? t(($) => $.office.dock.title)}
                  </span>
                  <time dateTime={message.created_at}>{messageDate(message.created_at, locale)}</time>
                </div>
                <p className="whitespace-pre-wrap break-words">
                  {messageContent(message, t(($) => $.office.dock.error))}
                </p>
              </article>
            ))}

            {conversation.pending ? (
              <p className="text-caption text-muted-foreground" role="status">
                {displayedStatusKey
                  ? t(($) => $.office.dock[displayedStatusKey])
                  : conversation.pending.status === "queued"
                    ? t(($) => $.office.dock.queued)
                    : t(($) => $.office.dock.loading)}
              </p>
            ) : null}

            {activeTaskMessages.map((message) => (
              <p
                key={`${message.task_id}:${message.seq}`}
                className="mr-auto max-w-[88%] rounded-xl border border-border bg-muted/40 p-3 text-body text-foreground"
              >
                {message.content}
              </p>
            ))}

            {conversation.isSending ? (
              <p className="flex items-center gap-2 text-caption text-muted-foreground" role="status">
                <Loader2 aria-hidden="true" className="size-3.5 animate-spin motion-reduce:animate-none" />
                {t(($) => $.office.dock.loading)}
              </p>
            ) : null}

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

            {conversation.messages.length === 0 && !conversation.pending && !conversation.sendError ? (
              <p className="py-4 text-center text-body text-muted-foreground">
                {t(($) => $.office.dock.empty)}
              </p>
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
