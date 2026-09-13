/* eslint-disable i18next/no-literal-string */
"use client";

import { useMemo } from "react";
import { ShieldCheck, AlertTriangle, MessageSquare } from "lucide-react";

import type {
  AutonomousProjectSnapshot,
  ProjectLeaderChangeRequest,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";

import { useT } from "../../../../i18n";

export interface DecisionGateWidgetProps {
  snapshot?: AutonomousProjectSnapshot | null;
  changeRequests?: ProjectLeaderChangeRequest[];
  canControl?: boolean;
  onApproveChange?: (id: string) => void;
  onRejectChange?: (id: string) => void;
  onResolveEscalation?: (id: string) => void;
  onOpenLeaderChat?: () => void;
  isApproving?: boolean;
  isRejecting?: boolean;
  className?: string;
}

export function DecisionGateWidget({
  snapshot,
  changeRequests = [],
  canControl = false,
  onApproveChange,
  onRejectChange,
  onResolveEscalation,
  onOpenLeaderChat,
  isApproving = false,
  isRejecting = false,
  className,
}: DecisionGateWidgetProps) {
  const { t } = useT("projects");

  const pendingApprovals = useMemo(
    () => changeRequests.filter((c) => c.state === "approval_required"),
    [changeRequests],
  );

  const pendingEscalations = useMemo(
    () => (snapshot?.escalations ?? []).filter((e) => e.status === "opened" || e.status === "pending"),
    [snapshot?.escalations],
  );

  const totalPending = pendingApprovals.length + pendingEscalations.length;
  const decisions = snapshot?.decisions ?? [];

  return (
    <div
      className={cn(
        "flex flex-col rounded-xl border bg-card p-4 shadow-xs transition-colors",
        totalPending > 0 && "border-amber-500/40 bg-amber-500/[0.02]",
        className,
      )}
    >
      {/* Header */}
      <div className="flex items-center justify-between gap-2 pb-3">
        <div className="flex items-center gap-2">
          <div
            className={cn(
              "flex size-7 items-center justify-center rounded-lg",
              totalPending > 0
                ? "bg-amber-500/10 text-amber-500"
                : "bg-primary/10 text-primary",
            )}
          >
            {totalPending > 0 ? (
              <AlertTriangle className="size-4" />
            ) : (
              <ShieldCheck className="size-4" />
            )}
          </div>
          <div>
            <h3 className="text-sm font-semibold leading-none text-foreground">
              {t(($) => $.cockpit.decision_gate_title)}
            </h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t(($) => $.cockpit.decision_gate_desc)}
            </p>
          </div>
        </div>

        {totalPending > 0 ? (
          <Badge variant="destructive" className="gap-1 font-mono text-[10px] animate-pulse">
            {totalPending} Action Needed
          </Badge>
        ) : (
          <Badge variant="secondary" className="gap-1 font-mono text-[10px]">
            <ShieldCheck className="size-3 text-emerald-500" />
            All Clear
          </Badge>
        )}
      </div>

      {/* Body */}
      {totalPending === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center rounded-lg border border-dashed p-6 text-center">
          <ShieldCheck className="size-6 text-emerald-500/80 mb-2" />
          <p className="text-xs text-muted-foreground max-w-[260px]">
            {t(($) => $.cockpit.decision_none)}
          </p>
          {onOpenLeaderChat && (
            <Button
              variant="ghost"
              size="sm"
              className="mt-3 gap-1.5 text-xs text-primary h-7 px-2.5"
              onClick={onOpenLeaderChat}
            >
              <MessageSquare className="size-3" />
              <span>{t(($) => $.cockpit.ask_pm)}</span>
            </Button>
          )}
        </div>
      ) : (
        <div className="flex-1 space-y-2.5 overflow-y-auto max-h-[260px] pr-1">
          {/* Pending Plan Change Requests */}
          {pendingApprovals.map((req) => (
            <div
              key={req.id}
              className="rounded-lg border border-amber-500/30 bg-card p-3 shadow-xs space-y-2.5"
            >
              <div className="flex items-start justify-between gap-2">
                <span className="text-xs font-semibold text-foreground truncate">
                  Plan Revision Proposal
                </span>
                <Badge variant="outline" className="text-[10px] border-amber-500/30 text-amber-600 dark:text-amber-400">
                  Requires Approval
                </Badge>
              </div>

              <p className="text-xs text-muted-foreground line-clamp-2 bg-muted/30 p-2 rounded-md border border-border/40 font-mono text-[11px]">
                {req.request_text}
              </p>

              {canControl && (
                <div className="flex items-center justify-end gap-2 pt-1">
                  {onRejectChange && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={isRejecting || isApproving}
                      onClick={() => onRejectChange(req.id)}
                      className="text-destructive hover:bg-destructive/10 text-xs h-7 px-2.5"
                    >
                      {t(($) => $.cockpit.reject_action)}
                    </Button>
                  )}
                  {onApproveChange && (
                    <Button
                      variant="default"
                      size="sm"
                      disabled={isApproving || isRejecting}
                      onClick={() => onApproveChange(req.id)}
                      className="bg-emerald-600 hover:bg-emerald-700 text-white text-xs h-7 px-2.5"
                    >
                      {t(($) => $.cockpit.approve_action)}
                    </Button>
                  )}
                </div>
              )}
            </div>
          ))}

          {/* Pending Escalations */}
          {pendingEscalations.map((esc) => (
            <div
              key={esc.id}
              className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 shadow-xs space-y-2"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="flex items-center gap-1.5 min-w-0">
                  <AlertTriangle className="size-3.5 text-destructive shrink-0" />
                  <span className="text-xs font-semibold text-foreground truncate">
                    {esc.category || "Escalation"}
                  </span>
                </div>
                <Badge variant="destructive" className="text-[10px]">
                  {esc.severity || "High"}
                </Badge>
              </div>

              <p className="text-xs text-muted-foreground line-clamp-2">
                {esc.summary}
              </p>

              {canControl && onResolveEscalation && (
                <div className="flex justify-end pt-1">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => onResolveEscalation(esc.id)}
                    className="text-xs h-7 px-2.5"
                  >
                    Resolve Escalation
                  </Button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {decisions.length > 0 ? (
        <details className="mt-3 border-t pt-3">
          <summary className="cursor-pointer text-xs font-medium text-primary">
            {t(($) => $.cockpit.decision_history, { count: decisions.length })}
          </summary>
          <div className="mt-2 space-y-2">
            {decisions.map((decision) => (
              <div key={decision.id} className="rounded-lg border bg-muted/20 p-2.5">
                <div className="text-xs font-medium text-foreground">
                  {decision.plan.summary || t(($) => $.cockpit.decision_summary_fallback)}
                </div>
                <div className="mt-1 text-[11px] text-muted-foreground">
                  {decision.planner_name} · {new Date(decision.created_at).toLocaleString()}
                </div>
              </div>
            ))}
          </div>
        </details>
      ) : null}
    </div>
  );
}
