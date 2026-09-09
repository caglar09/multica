/* eslint-disable i18next/no-literal-string, no-restricted-syntax */
"use client";

import { useMemo, useState, useRef, useEffect, useCallback } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Sparkles,
  Bot,
  ArrowUp,
  CheckCircle2,
  Paperclip,
  RefreshCw,
  Users,
  Brain,
  Gavel,
  Rocket,
  Moon,
  CloudLightning,
  Smartphone,
  Loader2,
  ChevronRight,
} from "lucide-react";
import { toast } from "sonner";
import { api, clientErrorMessage } from "@multica/core/api";
import { chatKeys, chatMessagesPageOptions, pendingChatTaskOptions } from "@multica/core/chat/queries";
import type {
  AutonomousProjectSnapshot,
  AutonomousTeamMember,
  AutonomousDecision,
  ChatMessage,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";

export interface ProjectManagerChangeProposal {
  summary?: string;
  operations?: Array<{
    kind?: string;
    title?: string;
    description?: string;
  }>;
}

export interface ProjectManagerChangeItem {
  id: string;
  state: string;
  request_text: string;
  proposal?: ProjectManagerChangeProposal | null;
  created_at: string;
}

export interface ProjectManagerHubProps {
  projectId: string;
  snapshot: AutonomousProjectSnapshot;
  leaderChatData: {
    leader: {
      id: string;
      name: string;
      status: string;
      avatar?: string | null;
    };
    session: {
      id: string;
    };
    can_chat: boolean;
  };
  leaderChangesData?: {
    items: ProjectManagerChangeItem[];
  };
  canControl?: boolean;
  onApproveChange?: (changeRequestId: string) => void;
  onRejectChange?: (changeRequestId: string) => void;
  isApproving?: boolean;
  isRejecting?: boolean;
  onOpenRuleModal?: () => void;
}

function formatMessageTime(timestamp?: string | null): string {
  if (!timestamp) {
    const now = new Date();
    return now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function ProjectManagerHub({
  projectId: _projectId,
  snapshot,
  leaderChatData,
  leaderChangesData,
  canControl = false,
  onApproveChange,
  onRejectChange,
  isApproving = false,
  isRejecting = false,
  onOpenRuleModal,
}: ProjectManagerHubProps) {
  const sessionId = leaderChatData.session.id;
  const canChat = leaderChatData.can_chat;
  const leader = leaderChatData.leader;
  const leaderName = leader.name || "Mika";

  const queryClient = useQueryClient();
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const messagesEndRef = useRef<HTMLDivElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  // Queries for chat messages and pending assistant task
  const { data: pages, isLoading, refetch: refetchMessages, isFetching } = useInfiniteQuery(
    chatMessagesPageOptions(sessionId),
  );
  const { data: pending } = useQuery(pendingChatTaskOptions(sessionId));

  const messages = useMemo(() => {
    return [...(pages?.pages ?? [])].reverse().flatMap((page) => page.messages) as ChatMessage[];
  }, [pages]);

  const scrollToBottom = useCallback((smooth = true) => {
    messagesEndRef.current?.scrollIntoView({ behavior: smooth ? "smooth" : "auto" });
  }, []);

  useEffect(() => {
    scrollToBottom(false);
  }, [messages.length, scrollToBottom]);

  const handleSendMessage = async (textToSend?: string) => {
    const content = (textToSend ?? draft).trim();
    if (!content || sending || !canChat) return;

    setSending(true);
    try {
      await api.sendChatMessage(sessionId, content, undefined, crypto.randomUUID());
      setDraft("");
      if (textareaRef.current) {
        textareaRef.current.style.height = "auto";
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: chatKeys.messagesPage(sessionId) }),
        queryClient.invalidateQueries({ queryKey: chatKeys.pendingTask(sessionId) }),
      ]);
      scrollToBottom();
    } catch (error) {
      toast.error(clientErrorMessage(error));
    } finally {
      setSending(false);
    }
  };

  const handleTextareaChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setDraft(e.target.value);
    e.target.style.height = "auto";
    e.target.style.height = `${Math.min(e.target.scrollHeight, 160)}px`;
  };

  // Pending change request proposal to display inside chat as interactive action card
  const pendingChange = useMemo(() => {
    return leaderChangesData?.items.find((c) => c.state === "approval_required");
  }, [leaderChangesData]);

  // Team members list (using snapshot or fallback to Stitch design preview)
  const teamMembers = useMemo(() => {
    if (snapshot.team?.members && snapshot.team.members.length > 0) {
      return snapshot.team.members;
    }
    // High-fidelity fallback roles matching Stitch template
    return [
      {
        role: "PM",
        family: "product",
        agent_id: "pm-1",
        agent_name: `${leaderName} (Product Lead)`,
        capabilities: ["requirements", "planning"],
        responsibilities: ["Kullanıcı isteklerini yönetir", "Planlama"],
        reason: "Core lead",
        active: true,
        current_task_id: null,
        current_task_title: "Kullanıcı isteklerini yönetiyor",
        current_task_status: "running",
        created_at: new Date().toISOString(),
      },
      {
        role: "FE",
        family: "frontend",
        agent_id: "fe-1",
        agent_name: "Frontend Geliştirici",
        capabilities: ["react", "tailwind"],
        responsibilities: ["Bileşen yapısı", "Grafik entegrasyonu"],
        reason: "UI Implementation",
        active: true,
        current_task_id: null,
        current_task_title: "Bileşen yapısı & grafik entegrasyonu",
        current_task_status: "awaiting_approval",
        created_at: new Date().toISOString(),
      },
      {
        role: "QA",
        family: "qa",
        agent_id: "qa-1",
        agent_name: "QA & Test Uzmanı",
        capabilities: ["playwright", "e2e"],
        responsibilities: ["E2E senaryoları", "Doğrulama"],
        reason: "Quality Assurance",
        active: true,
        current_task_id: null,
        current_task_title: "E2E Playwright senaryoları",
        current_task_status: "idle",
        created_at: new Date().toISOString(),
      },
      {
        role: "CR",
        family: "review",
        agent_id: "cr-1",
        agent_name: "Kod İnceleyici",
        capabilities: ["security", "linter"],
        responsibilities: ["Statik analiz", "Güvenlik"],
        reason: "Code Review",
        active: true,
        current_task_id: null,
        current_task_title: "Statik analiz & güvenlik denetimi",
        current_task_status: "idle",
        created_at: new Date().toISOString(),
      },
    ] as AutonomousTeamMember[];
  }, [snapshot.team?.members, leaderName]);

  // Project decisions list (using snapshot or fallback to Stitch design decisions)
  const decisions = useMemo(() => {
    if (snapshot.decisions && snapshot.decisions.length > 0) {
      return snapshot.decisions;
    }
    return [
      {
        id: "KARAR-02",
        source_type: "architecture",
        source_id: "arch-2",
        source_revision: "1",
        planner_name: leaderName,
        planner_model: "autonomous",
        plan: {
          intent: "VERİ ENTEGRASYONU",
          summary: "5 günlük tahmin grafiği için ek kütüphane yerine hafif SVG eğrileri tercih edildi; bundle boyutu minimum tutulacak.",
        },
        created_at: "Bugün 14:05",
      },
      {
        id: "KARAR-01",
        source_type: "architecture",
        source_id: "arch-1",
        source_revision: "1",
        planner_name: leaderName,
        planner_model: "autonomous",
        plan: {
          intent: "EKİP YAPISI",
          summary: "Tek ekranlı SPA projesi olduğu için backend ajanına ihtiyaç duyulmadı, sadece Frontend + QA + Reviewer kadrosu kuruldu.",
        },
        created_at: "Bugün 13:58",
      },
    ] as unknown as AutonomousDecision[];
  }, [snapshot.decisions, leaderName]);

  // Starter prompts for new sessions
  const starterPrompts = [
    "Sprint 1 başlangıç paketini ve iş listesini hazırla",
    "Arayüz için varsayılan Dark Mode ve responsive kurallarını ekle",
    "Open-Meteo API entegrasyonu ve QA senaryolarını doğrula",
  ];

  return (
    <div className="flex flex-col lg:flex-row h-full min-h-[640px] max-h-[calc(100vh-180px)] rounded-xl border border-border bg-card/60 backdrop-blur-sm shadow-sm overflow-hidden">
      {/* =========================================================================
          LEFT COLUMN (55-60%): Mika Conversational Station (Chat & Command Hub)
      ========================================================================= */}
      <section className="flex-1 flex flex-col min-w-0 border-b lg:border-b-0 lg:border-r border-border bg-background/50 relative">
        {/* Chat Subheader */}
        <div className="px-5 py-3.5 border-b border-border/80 bg-card/40 flex items-center justify-between shrink-0">
          <div className="flex items-center gap-3 min-w-0">
            <div className="relative shrink-0">
              <div className="w-8 h-8 rounded-lg bg-primary/10 border border-primary/25 flex items-center justify-center text-primary shadow-2xs">
                <Bot className="size-4" />
              </div>
              <span className="absolute -bottom-0.5 -right-0.5 size-2.5 rounded-full bg-emerald-500 ring-2 ring-background animate-pulse" />
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold text-foreground truncate">
                  {leaderName} (Product Manager Agent)
                </span>
                <span className="hidden sm:inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] font-mono font-medium bg-muted text-emerald-500 border border-emerald-500/20">
                  <span className="size-1.5 rounded-full bg-emerald-500" />
                  Otonom İş İstasyonu
                </span>
              </div>
              <p className="text-[11px] text-muted-foreground truncate">
                Fikrinizi projeye ve otonom iş paketlerine dönüştürür
              </p>
            </div>
          </div>

            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="h-8 w-8 text-muted-foreground hover:text-foreground"
                    onClick={() => void refetchMessages()}
                    disabled={isFetching}
                  >
                    <RefreshCw className={cn("size-3.5", isFetching && "animate-spin")} />
                  </Button>
                }
              />
              <TooltipContent side="bottom">Sohbeti Yenile</TooltipContent>
            </Tooltip>
        </div>

        {/* Message Stream */}
        <div className="flex-1 overflow-y-auto p-4 md:p-6 space-y-5">
          {isLoading && (
            <div className="space-y-4">
              <Skeleton className="h-14 w-2/3" />
              <Skeleton className="h-20 w-3/4 ml-auto" />
              <Skeleton className="h-28 w-4/5" />
            </div>
          )}

          {/* Empty state with starter prompts */}
          {!isLoading && messages.length === 0 && (
            <div className="py-8 px-4 flex flex-col items-center justify-center text-center max-w-md mx-auto space-y-4">
              <div className="w-12 h-12 rounded-2xl bg-primary/10 border border-primary/20 flex items-center justify-center text-primary shadow-xs">
                <Sparkles className="size-6" />
              </div>
              <div>
                <h3 className="text-base font-semibold text-foreground">
                  {leaderName} ile Çalışmaya Başlayın
                </h3>
                <p className="text-xs text-muted-foreground mt-1 leading-relaxed">
                  Projeniz için bir gereksinim iletin, mimari kural ekleyin veya ekibi kurup sprint sürecini başlatmasını isteyin.
                </p>
              </div>

              <div className="w-full space-y-2 pt-2 text-left">
                <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">
                  Örnek İstekler
                </p>
                {starterPrompts.map((prompt, idx) => (
                  <button
                    key={idx}
                    type="button"
                    onClick={() => void handleSendMessage(prompt)}
                    className="w-full p-2.5 rounded-lg border border-border/70 bg-card/60 hover:bg-accent/70 hover:border-primary/40 text-xs text-foreground transition-colors flex items-center justify-between group text-left cursor-pointer"
                  >
                    <span className="line-clamp-1">{prompt}</span>
                    <ChevronRight className="size-3.5 text-muted-foreground group-hover:text-primary transition-transform group-hover:translate-x-0.5 shrink-0 ml-2" />
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Render Messages */}
          {messages.map((message) => {
            const isUser = message.role === "user";

            if (isUser) {
              return (
                <div key={message.id} className="flex justify-end gap-3 items-start pl-10 md:pl-16">
                  <div className="flex flex-col items-end max-w-xl">
                    <div className="flex items-center gap-2 mb-1">
                      <span className="text-xs font-medium text-foreground">Siz</span>
                      <span className="text-[11px] font-mono text-muted-foreground">
                        {formatMessageTime(message.created_at)}
                      </span>
                    </div>
                    <div className="px-4 py-3 rounded-2xl rounded-tr-xs bg-primary text-primary-foreground text-sm shadow-xs leading-relaxed whitespace-pre-wrap">
                      {message.content}
                    </div>
                  </div>
                  <div className="w-8 h-8 rounded-full bg-primary/20 text-primary flex items-center justify-center font-semibold text-xs shrink-0 select-none">
                    U
                  </div>
                </div>
              );
            }

            // Assistant / Product Manager message
            return (
              <div key={message.id} className="flex gap-3 items-start pr-4 md:pr-12">
                <div className="w-8 h-8 rounded-lg bg-primary/10 border border-primary/25 flex items-center justify-center text-primary shrink-0 shadow-2xs">
                  <Bot className="size-4" />
                </div>
                <div className="flex flex-col max-w-2xl w-full space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-semibold text-foreground">{leaderName}</span>
                    <span className="px-1.5 py-0.2 rounded text-[10px] font-mono font-medium bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20">
                      Plan Hazır
                    </span>
                    <span className="text-[11px] font-mono text-muted-foreground">
                      {formatMessageTime(message.created_at)}
                    </span>
                  </div>

                  <div className="p-4 rounded-2xl rounded-tl-xs bg-card border border-border text-foreground text-sm space-y-3 leading-relaxed shadow-xs">
                    <div className="whitespace-pre-wrap">{message.content}</div>

                    {/* Integrated Action Card if there is an active proposal pending */}
                    {pendingChange && (
                      <div className="mt-3 p-3.5 rounded-xl bg-background border border-primary/30 space-y-3 shadow-inner">
                        <div className="flex items-center justify-between pb-2 border-b border-border/60">
                          <div className="flex items-center gap-2">
                            <Rocket className="size-4 text-primary" />
                            <span className="text-xs font-semibold text-foreground">
                              {pendingChange.proposal?.summary || "Sprint 1 Başlangıç Paketi"}
                            </span>
                          </div>
                          <Badge variant="outline" className="text-[10px] font-mono text-primary border-primary/30 bg-primary/5">
                            {pendingChange.proposal?.operations?.length
                              ? `${pendingChange.proposal.operations.length} Görev Tanımlandı`
                              : "Onay Bekliyor"}
                          </Badge>
                        </div>

                        {/* Checklist items */}
                        <div className="space-y-2 text-xs text-muted-foreground">
                          {pendingChange.proposal?.operations && pendingChange.proposal.operations.length > 0 ? (
                            pendingChange.proposal.operations.map((op, opIdx) => (
                              <div key={opIdx} className="flex items-start gap-2">
                                <CheckCircle2 className="size-3.5 text-emerald-500 shrink-0 mt-0.5" />
                                <span>
                                  <strong className="text-foreground">{op.title || op.kind}:</strong> {op.description || op.title}
                                </span>
                              </div>
                            ))
                          ) : (
                            <>
                              <div className="flex items-center gap-2">
                                <CheckCircle2 className="size-3.5 text-emerald-500 shrink-0" />
                                <span><strong>Frontend:</strong> React + Tailwind ile arama çubuğu ve 5 günlük tahmin grafiği</span>
                              </div>
                              <div className="flex items-center gap-2">
                                <CheckCircle2 className="size-3.5 text-emerald-500 shrink-0" />
                                <span><strong>Veri Kaynağı:</strong> Open-Meteo API entegrasyonu (ücretsiz, key gerekmez)</span>
                              </div>
                              <div className="flex items-center gap-2">
                                <CheckCircle2 className="size-3.5 text-emerald-500 shrink-0" />
                                <span><strong>QA:</strong> Playwright ile responsive görsel ve senaryo testleri</span>
                              </div>
                            </>
                          )}
                        </div>

                        {/* Interactive CTA Buttons */}
                        {canControl && (
                          <div className="pt-1 flex items-center gap-2">
                            <Button
                              type="button"
                              size="sm"
                              disabled={isApproving || isRejecting}
                              onClick={() => onApproveChange?.(pendingChange.id)}
                              className="flex-1 text-xs font-semibold h-8 bg-primary hover:bg-primary/90 text-primary-foreground shadow-xs gap-1.5"
                            >
                              <Rocket className="size-3.5" />
                              Ekibi ve Planı Onayla (Süreci Başlat)
                            </Button>
                            <Button
                              type="button"
                              size="sm"
                              variant="outline"
                              disabled={isApproving || isRejecting}
                              onClick={() => onRejectChange?.(pendingChange.id)}
                              className="text-xs h-8"
                            >
                              Düzenle
                            </Button>
                          </div>
                        )}

                        <p className="text-[11px] text-muted-foreground italic">
                          💡 Onayladığınızda ajanlar eş zamanlı olarak kodlama ve test sürecine başlayacaktır.
                        </p>
                      </div>
                    )}
                  </div>
                </div>
              </div>
            );
          })}

          {/* Pending / Thinking indicator */}
          {pending?.task_id && (
            <div className="flex items-center gap-2.5 text-xs text-muted-foreground animate-pulse pl-11 py-1">
              <Loader2 className="size-3.5 animate-spin text-primary" />
              <span>{leaderName} düşünüyor ve plan hazırlıyor…</span>
            </div>
          )}

          <div ref={messagesEndRef} />
        </div>

        {/* Modern Airy Prompt Input Bar */}
        <div className="p-3 md:p-4 border-t border-border bg-card/40 shrink-0">
          <div className="rounded-xl border border-border bg-background focus-within:border-primary/50 focus-within:ring-1 focus-within:ring-primary/20 transition-all p-2.5 shadow-2xs">
            <textarea
              ref={textareaRef}
              value={draft}
              onChange={handleTextareaChange}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  void handleSendMessage();
                }
              }}
              disabled={!canChat || sending}
              placeholder={`${leaderName}'ya yeni bir kural söyleyin veya gereksinim ekleyin... (Örn: Şehir aramasında favorilere ekleme özelliği olsun)`}
              rows={2}
              className="w-full bg-transparent border-0 resize-none text-sm text-foreground placeholder:text-muted-foreground outline-none focus:ring-0 p-1 leading-relaxed disabled:cursor-not-allowed disabled:opacity-60"
            />
            <div className="flex items-center justify-between pt-2 border-t border-border/50 mt-1">
              <div className="flex items-center gap-1.5 text-muted-foreground">
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <button
                        type="button"
                        disabled={!canChat}
                        className="p-1.5 rounded-lg hover:bg-muted hover:text-foreground transition-colors disabled:opacity-50 cursor-pointer"
                      >
                        <Paperclip className="size-4" />
                      </button>
                    }
                  />
                  <TooltipContent side="top">Dosya veya Taslak Ekle</TooltipContent>
                </Tooltip>

                <Tooltip>
                  <TooltipTrigger
                    render={
                      <button
                        type="button"
                        onClick={() => onOpenRuleModal?.()}
                        className="p-1.5 rounded-lg hover:bg-muted hover:text-foreground transition-colors cursor-pointer"
                      >
                        <Brain className="size-4" />
                      </button>
                    }
                  />
                  <TooltipContent side="top">Kural Tanımla / Bellek</TooltipContent>
                </Tooltip>

                <span className="text-[11px] font-mono text-muted-foreground ml-1 hidden sm:inline select-none">
                  {leaderName} Context: v1.4
                </span>
              </div>

              <div className="flex items-center gap-2">
                <span className="text-[11px] font-mono text-muted-foreground hidden md:inline select-none">
                  Enter ile gönder
                </span>
                <Button
                  type="button"
                  size="icon-sm"
                  onClick={() => void handleSendMessage()}
                  disabled={!draft.trim() || !canChat || sending}
                  className="h-8 w-8 rounded-lg bg-primary hover:bg-primary/90 text-primary-foreground transition-colors shadow-2xs"
                >
                  <ArrowUp className="size-4" />
                </Button>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* =========================================================================
          RIGHT COLUMN (40-45%): Proje Durumu & Canlı Ajanlar (Context Hub)
      ========================================================================= */}
      <aside className="w-full lg:w-[420px] xl:w-[460px] flex flex-col bg-muted/15 overflow-y-auto p-4 md:p-5 space-y-4 shrink-0">
        {/* Panel Header */}
        <div className="flex items-center justify-between pb-2 border-b border-border">
          <div>
            <h3 className="font-semibold text-sm text-foreground">Proje Durumu &amp; Canlı Ajanlar</h3>
            <p className="text-xs text-muted-foreground">
              {leaderName}&apos;nın organize ettiği canlı ekip ve karar defteri
            </p>
          </div>
          <span className="px-2.5 py-1 rounded-full text-xs font-mono bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/20 flex items-center gap-1.5 shrink-0">
            <span className="size-2 rounded-full bg-emerald-500 animate-pulse" />
            Senkronize
          </span>
        </div>

        {/* 1. Ekip Durumu: Ajanların Sade Durum Çubuğu */}
        <div className="p-4 rounded-xl bg-card border border-border shadow-2xs space-y-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Users className="size-4 text-primary" />
              <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground font-mono">
                Otonom Ekip Durumu
              </h4>
            </div>
            <span className="text-xs text-muted-foreground font-mono">
              {teamMembers.length} Rol Atandı
            </span>
          </div>

          <div className="space-y-2">
            {teamMembers.map((member, idx) => {
              const roleInitials = member.role.slice(0, 2).toUpperCase();
              const isLead = idx === 0 || member.role.toLowerCase().includes("pm");
              const isRunning = member.current_task_status === "running" || isLead;
              const isWaiting = member.current_task_status === "awaiting_approval";

              return (
                <div
                  key={member.agent_id || idx}
                  className="p-2.5 rounded-lg bg-background/80 border border-border/80 flex items-center justify-between gap-2"
                >
                  <div className="flex items-center gap-2.5 min-w-0">
                    <div
                      className={cn(
                        "size-7 rounded-md flex items-center justify-center font-bold text-[11px] shrink-0",
                        isLead
                          ? "bg-primary/20 text-primary border border-primary/30"
                          : "bg-muted text-muted-foreground",
                      )}
                    >
                      {roleInitials}
                    </div>
                    <div className="min-w-0">
                      <div className="text-xs font-medium text-foreground truncate">
                        {member.agent_name}
                      </div>
                      <div className="text-[11px] text-muted-foreground truncate">
                        {member.current_task_title || member.responsibilities?.[0] || "Göreve hazır"}
                      </div>
                    </div>
                  </div>

                  <span
                    className={cn(
                      "px-2 py-0.5 rounded text-[11px] font-mono shrink-0 flex items-center gap-1 border",
                      isRunning
                        ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/20"
                        : isWaiting
                        ? "bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/20"
                        : "bg-muted text-muted-foreground border-transparent",
                    )}
                  >
                    <span
                      className={cn(
                        "size-1.5 rounded-full",
                        isRunning
                          ? "bg-emerald-500"
                          : isWaiting
                          ? "bg-amber-500"
                          : "bg-muted-foreground/60",
                      )}
                    />
                    {isRunning ? "Çalışıyor" : isWaiting ? "Beklemede (Onay bekliyor)" : "Beklemede"}
                  </span>
                </div>
              );
            })}
          </div>
        </div>

        {/* 2. Proje Belleği (Brain): Temel Kurallar Sade Maddeler Halinde */}
        <div className="p-4 rounded-xl bg-card border border-border shadow-2xs space-y-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Brain className="size-4 text-sky-500" />
              <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground font-mono">
                Proje Belleği (Brain)
              </h4>
            </div>
            <button
              type="button"
              onClick={() => onOpenRuleModal?.()}
              className="text-[11px] text-primary hover:underline cursor-pointer font-mono"
            >
              + Kural Ekle
            </button>
          </div>

          <ul className="space-y-2 text-xs">
            <li className="flex items-start gap-2.5 p-2 rounded-lg bg-background/80 border border-border/60">
              <Moon className="size-4 text-sky-500 shrink-0 mt-0.5" />
              <div>
                <span className="font-medium text-foreground">Tasarım Standardı:</span>
                <p className="text-muted-foreground text-[11px] mt-0.5">
                  Kullanıcı tercihi gereği arayüz varsayılan olarak &apos;Dark Mode&apos; çalışacak.
                </p>
              </div>
            </li>
            <li className="flex items-start gap-2.5 p-2 rounded-lg bg-background/80 border border-border/60">
              <CloudLightning className="size-4 text-emerald-500 shrink-0 mt-0.5" />
              <div>
                <span className="font-medium text-foreground">Hava Durumu API:</span>
                <p className="text-muted-foreground text-[11px] mt-0.5">
                  Open-Meteo API kullanılacak. Rate limit ve API anahtarı gerektirmez.
                </p>
              </div>
            </li>
            <li className="flex items-start gap-2.5 p-2 rounded-lg bg-background/80 border border-border/60">
              <Smartphone className="size-4 text-primary shrink-0 mt-0.5" />
              <div>
                <span className="font-medium text-foreground">Duyarlılık (Responsive):</span>
                <p className="text-muted-foreground text-[11px] mt-0.5">
                  Mobil ve masaüstü görünümlerde grafik yatay kaydırılabilir kalacak.
                </p>
              </div>
            </li>
          </ul>
        </div>

        {/* 3. Son Kararlar (Architecture Ledger): Net 1-2 Cümlelik Özetler */}
        <div className="p-4 rounded-xl bg-card border border-border shadow-2xs space-y-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Gavel className="size-4 text-amber-500" />
              <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground font-mono">
                {leaderName}&apos;nın Son Kararları
              </h4>
            </div>
            <span className="text-[11px] text-muted-foreground font-mono">
              {decisions.length} Karar Kayıtlı
            </span>
          </div>

          <div className="space-y-2">
            {decisions.map((decision, dIdx) => (
              <div
                key={decision.id || dIdx}
                className={cn(
                  "p-3 rounded-lg bg-background/80 border border-border/70 border-l-3 space-y-1",
                  dIdx % 2 === 0 ? "border-l-emerald-500" : "border-l-primary",
                )}
              >
                <div className="flex items-center justify-between text-[11px]">
                  <span className={cn(
                    "font-mono font-semibold",
                    dIdx % 2 === 0 ? "text-emerald-600 dark:text-emerald-400" : "text-primary"
                  )}>
                    {decision.id} · {decision.plan?.intent || "KARAR"}
                  </span>
                  <span className="text-muted-foreground text-[10px]">
                    {decision.created_at || "Bugün"}
                  </span>
                </div>
                <p className="text-xs text-foreground leading-snug">
                  &ldquo;{decision.plan?.summary || "Karar detayı kaydedildi."}&rdquo;
                </p>
              </div>
            ))}
          </div>
        </div>
      </aside>
    </div>
  );
}
