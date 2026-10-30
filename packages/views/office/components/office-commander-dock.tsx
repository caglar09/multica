import { useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import {
  Crown,
  Sparkles,
  Bot,
  HelpCircle,
  CornerDownLeft,
  ChevronDown,
} from "lucide-react";
import type { OfficeAgentDesk, OfficeChatMessage } from "../types";

interface OfficeCommanderDockProps {
  desks?: OfficeAgentDesk[];
  selectedDesk?: OfficeAgentDesk | null;
  onSelectDesk?: (desk: OfficeAgentDesk | null) => void;
  onSendMessage?: (payload: { targetAgentId?: string; targetAgentName?: string; text: string }) => Promise<void> | void;
  recentMessages?: OfficeChatMessage[];
  className?: string;
}

const QUICK_PROMPTS = [
  { label: "📊 Sprint Durumu?", prompt: "Projedeki genel işleyiş ve aktif sprint durumu nedir?" },
  { label: "🧪 Testler Geçti mi?", prompt: "QA doğrulama ve kalite kontrolleri ne durumda?" },
  { label: "🎯 Sıradaki Görev?", prompt: "Bir sonraki tamamlanması gereken kritik task hangisi?" },
  { label: "⚠️ Blokaj Var mı?", prompt: "Şu an herhangi bir ajanda blokaj veya bekleyen karar var mı?" },
];

export function OfficeCommanderDock({
  desks = [],
  selectedDesk,
  onSelectDesk,
  onSendMessage,
  recentMessages: _recentMessages = [],
  className,
}: OfficeCommanderDockProps) {
  const [inputText, setInputText] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [lastResponse, setLastResponse] = useState<string | null>(null);
  const [isDropdownOpen, setIsDropdownOpen] = useState(false);

  // Default target is the Project Manager (leader)
  const leaderDesk = desks.find((d) => d.isLeader) || desks[0];
  const targetDesk = selectedDesk || leaderDesk;
  const isTargetingLeader = targetDesk?.id === leaderDesk?.id;

  const handleSend = async (textToSend?: string) => {
    const text = (textToSend || inputText).trim();
    if (!text || isSubmitting) return;

    setIsSubmitting(true);
    setInputText("");

    try {
      if (onSendMessage) {
        await onSendMessage({
          targetAgentId: targetDesk?.agentId,
          targetAgentName: targetDesk ? targetDesk.agentName : "Project Manager",
          text,
        });
      }

      // Simulated immediate response from Project Manager or selected agent
      const simulatedReply = isTargetingLeader
        ? `[Project Manager]: "${text}" sorunuz alındı. Ekipler koordinasyon halinde, aktif sprint planlandığı gibi ilerliyor ve blokaj bulunmuyor.`
        : `[${targetDesk?.agentName}]: "${text}" sorunuz için: Şu anki görevim "${targetDesk?.activeTask?.title || targetDesk?.currentTaskTitle || "geliştirme"}" üzerinde çalışıyorum.`;

      setLastResponse(simulatedReply);
    } catch {
      setLastResponse("Yanıt alınırken bir hata oluştu.");
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div
      className={cn(
        "pointer-events-auto flex flex-col items-center gap-2 max-w-2xl w-full px-4 select-none",
        className,
      )}
    >
      {/* Latest Response Flash Card */}
      {lastResponse && (
        <div className="flex items-center gap-2 rounded-xl border border-sky-500/30 bg-slate-950/90 px-3 py-1.5 text-xs text-sky-200 shadow-xl backdrop-blur-md animate-in fade-in slide-in-from-bottom-2">
          <Sparkles className="size-3.5 text-sky-400 shrink-0 animate-pulse" />
          <span className="truncate flex-1 font-mono text-[11px]">{lastResponse}</span>
          <button
            type="button"
            onClick={() => setLastResponse(null)}
            className="text-[10px] text-slate-400 hover:text-white"
          >
            ✕
          </button>
        </div>
      )}

      {/* Main Commander Bar */}
      <div className="relative flex items-center gap-2 w-full rounded-2xl border border-slate-700/80 bg-slate-900/95 p-1.5 pl-3 shadow-2xl backdrop-blur-xl transition-all focus-within:border-sky-500/70 focus-within:ring-2 focus-within:ring-sky-500/20">
        {/* Target Switcher: Project Manager (Default) or Selected Agent Dropdown */}
        <div className="relative flex items-center gap-1.5 shrink-0">
          <button
            type="button"
            onClick={() => setIsDropdownOpen((prev) => !prev)}
            className={cn(
              "flex items-center gap-1.5 rounded-xl px-2.5 py-1 text-xs font-semibold transition-all border",
              isTargetingLeader
                ? "bg-amber-500/20 text-amber-300 border-amber-500/50 shadow-xs shadow-amber-500/20 hover:bg-amber-500/30"
                : "bg-sky-500/15 text-sky-300 border-sky-500/40 hover:bg-sky-500/25",
            )}
            title="Soru sorulacak ajanı değiştirin"
          >
            {isTargetingLeader ? (
              <Crown className="size-3.5 text-amber-400 fill-amber-400" />
            ) : (
              <Bot className="size-3.5 text-sky-400" />
            )}
            <span className="max-w-[120px] truncate">
              {isTargetingLeader ? "Project Manager" : targetDesk?.agentName}
            </span>
            <ChevronDown className="size-3 opacity-60 ml-0.5" />
          </button>

          {/* Agent Selection Dropdown Menu */}
          {isDropdownOpen && (
            <div className="absolute left-0 bottom-full mb-2 w-64 max-h-72 overflow-y-auto rounded-xl border border-slate-700 bg-slate-950/95 p-1.5 shadow-2xl backdrop-blur-xl z-50 animate-in fade-in slide-in-from-bottom-2">
              <div className="px-2 py-1 text-[10px] font-bold text-slate-400 uppercase tracking-wider border-b border-slate-800">
                Hedef Ajanı Seç
              </div>

              {/* Leader Option */}
              {leaderDesk && (
                <button
                  type="button"
                  onClick={() => {
                    onSelectDesk?.(leaderDesk);
                    setIsDropdownOpen(false);
                  }}
                  className={cn(
                    "flex items-center gap-2 w-full rounded-lg px-2 py-1.5 text-left text-xs transition-colors mt-1",
                    isTargetingLeader
                      ? "bg-amber-500/20 text-amber-300 font-semibold"
                      : "text-slate-300 hover:bg-slate-800",
                  )}
                >
                  <Crown className="size-3.5 text-amber-400 fill-amber-400 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="truncate font-semibold">{leaderDesk.agentName}</div>
                    <div className="text-[10px] text-amber-400/80">Proje Yöneticisi & Lider</div>
                  </div>
                </button>
              )}

              {/* Other Team Members */}
              {desks
                .filter((d) => d.id !== leaderDesk?.id)
                .map((d) => {
                  const isSelected = targetDesk?.id === d.id;
                  return (
                    <button
                      key={d.id}
                      type="button"
                      onClick={() => {
                        onSelectDesk?.(d);
                        setIsDropdownOpen(false);
                      }}
                      className={cn(
                        "flex items-center gap-2 w-full rounded-lg px-2 py-1.5 text-left text-xs transition-colors mt-0.5",
                        isSelected
                          ? "bg-sky-500/20 text-sky-300 font-semibold"
                          : "text-slate-300 hover:bg-slate-800",
                      )}
                    >
                      <Bot className="size-3.5 text-sky-400 shrink-0" />
                      <div className="flex-1 min-w-0">
                        <div className="truncate">{d.agentName}</div>
                        <div className="text-[10px] text-slate-400 truncate">
                          {d.categoryLabel || d.role}
                        </div>
                      </div>
                    </button>
                  );
                })}
            </div>
          )}
        </div>

        {/* Vertical divider */}
        <div className="h-5 w-px bg-slate-800 shrink-0" />

        {/* Input Field */}
        <input
          type="text"
          value={inputText}
          onChange={(e) => setInputText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              handleSend();
            }
          }}
          placeholder={
            isTargetingLeader
              ? "Project Manager'a sor (örn: 'Sprint durumu nedir?')..."
              : `${targetDesk?.agentName} ajanına doğrudan soru sor...`
          }
          className="flex-1 bg-transparent text-xs text-slate-100 placeholder:text-slate-500 focus:outline-hidden px-1"
        />

        {/* Send Action */}
        <Button
          size="sm"
          disabled={!inputText.trim() || isSubmitting}
          onClick={() => handleSend()}
          className="h-8 gap-1.5 rounded-xl bg-sky-600 hover:bg-sky-500 text-white font-medium text-xs px-3 shadow-md shadow-sky-600/30"
        >
          <span>Sor</span>
          <CornerDownLeft className="size-3 opacity-70" />
        </Button>
      </div>

      {/* Quick Question Pills */}
      <div className="flex items-center gap-1.5 overflow-x-auto max-w-full pb-0.5">
        <span className="text-[10px] text-slate-500 font-medium shrink-0 flex items-center gap-1">
          <HelpCircle className="size-2.5" />
          <span>Hızlı:</span>
        </span>
        {QUICK_PROMPTS.map((qp, idx) => (
          <button
            key={idx}
            type="button"
            onClick={() => handleSend(qp.prompt)}
            className="rounded-full bg-slate-800/80 hover:bg-slate-700/80 border border-slate-700/50 px-2.5 py-0.5 text-[10px] text-slate-300 hover:text-white transition-colors shrink-0"
          >
            {qp.label}
          </button>
        ))}
      </div>
    </div>
  );
}
