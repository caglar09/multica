"use client";

import { useEffect, useMemo, useState } from "react";
import { Sparkles, Check, RotateCcw } from "lucide-react";

import type {
  AutonomousProjectSnapshot,
  AutonomousRoleRuntimeAssignment,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";

import { useT } from "../../../../i18n";

export interface SquadBootstrappingWidgetProps {
  snapshot: AutonomousProjectSnapshot;
  canControl?: boolean;
  isConfirming?: boolean;
  onConfirmTeam: (assignments: AutonomousRoleRuntimeAssignment[]) => void;
  onReplanTeam?: () => void;
  className?: string;
}

export function SquadBootstrappingWidget({
  snapshot,
  canControl = true,
  isConfirming = false,
  onConfirmTeam,
  onReplanTeam,
  className,
}: SquadBootstrappingWidgetProps) {
  const { t } = useT("projects");
  const draft = snapshot.draft;
  const roles = useMemo(() => draft?.plan?.roles ?? [], [draft?.plan?.roles]);

  const [assignments, setAssignments] = useState<
    Record<string, { runtime_id: string; model: string }>
  >({});

  // Initialize assignments with fallback runtime and suggested models
  useEffect(() => {
    if (!draft) {
      setAssignments({});
      return;
    }
    const fallbackRuntime =
      draft.default_runtime_id ??
      snapshot.runtimes.find((r) => r.status === "online")?.id ??
      snapshot.runtimes[0]?.id ??
      "";

    const next: Record<string, { runtime_id: string; model: string }> = {};
    for (const role of draft.plan?.roles ?? []) {
      next[role.role] = {
        runtime_id: fallbackRuntime,
        model: draft.planner_model ?? "",
      };
    }
    setAssignments(next);
  }, [draft, snapshot.runtimes]);

  const handleConfirm = () => {
    const payload: AutonomousRoleRuntimeAssignment[] = roles.map((role) => ({
      role: role.role,
      runtime_id: assignments[role.role]?.runtime_id ?? "",
      model: assignments[role.role]?.model || undefined,
      skill_mode: "inherit",
      skill_ids: [],
    }));
    onConfirmTeam(payload);
  };

  if (!draft) return null;

  return (
    <div
      className={cn(
        "rounded-xl border border-amber-500/40 bg-amber-500/[0.03] p-4 md:p-5 shadow-xs transition-colors",
        className,
      )}
    >
      {/* Header */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between pb-4 border-b border-border/50">
        <div className="flex items-start gap-3">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <Sparkles className="size-4.5" />
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold text-foreground">
                {t(($) => $.cockpit.bootstrapping_title)}
              </h3>
              <Badge
                variant="outline"
                className="border-amber-500/30 text-amber-600 dark:text-amber-400 text-[10px] font-mono"
              >
                {t(($) => $.cockpit.bootstrapping_awaiting_approval)}
              </Badge>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.cockpit.bootstrapping_desc)}
            </p>
          </div>
        </div>

        {canControl && (
          <div className="flex items-center gap-2 self-end sm:self-auto shrink-0">
            {onReplanTeam && (
              <Button
                variant="outline"
                size="sm"
                className="text-xs h-8 px-2.5"
                onClick={onReplanTeam}
                disabled={isConfirming}
              >
                <RotateCcw className="size-3 mr-1" />
                <span>{t(($) => $.cockpit.bootstrapping_replan_button)}</span>
              </Button>
            )}

            <Button
              variant="default"
              size="sm"
              className="bg-emerald-600 hover:bg-emerald-700 text-white text-xs h-8 px-3 font-medium"
              onClick={handleConfirm}
              disabled={isConfirming || roles.length === 0}
            >
              <Check className="size-3.5 mr-1" />
              <span>{t(($) => $.cockpit.bootstrapping_confirm_button)}</span>
            </Button>
          </div>
        )}
      </div>

      {/* Proposed Roles Grid */}
      <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {roles.map((role) => (
          <div
            key={role.role}
            className="flex flex-col justify-between rounded-lg border bg-card p-3 shadow-xs space-y-3"
          >
            <div>
              <div className="flex items-start justify-between gap-2">
                <div className="flex items-center gap-2">
                  <div className="flex size-6 items-center justify-center rounded bg-primary/10 text-primary font-mono text-xs font-bold uppercase">
                    {role.role.slice(0, 2)}
                  </div>
                  <div>
                    <h4 className="text-xs font-semibold text-foreground">
                      {role.display_name || role.role}
                    </h4>
                    <span className="text-[10px] font-mono text-muted-foreground capitalize">
                      {role.family}
                    </span>
                  </div>
                </div>

                {draft.planner_model && (
                  <Badge variant="secondary" className="font-mono text-[9px] px-1.5 py-0 h-4.5">
                    {draft.planner_model}
                  </Badge>
                )}
              </div>

              {role.responsibilities && role.responsibilities.length > 0 && (
                <p className="mt-2.5 text-[11px] text-muted-foreground line-clamp-2 bg-muted/20 p-2 rounded-md border border-border/30">
                  {role.responsibilities.join(", ")}
                </p>
              )}
            </div>

            {role.capabilities && role.capabilities.length > 0 && (
              <div className="flex flex-wrap gap-1 pt-1">
                {role.capabilities.slice(0, 3).map((cap) => (
                  <span
                    key={cap}
                    className="rounded bg-muted/40 px-1.5 py-0.5 text-[9px] font-mono text-muted-foreground"
                  >
                    {cap}
                  </span>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
