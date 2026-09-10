/* eslint-disable i18next/no-literal-string */
"use client";

import { useMemo } from "react";
import { Brain, ShieldCheck, ArrowRight, BookOpen } from "lucide-react";

import type { AutonomousProjectSnapshot } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";

import { useT } from "../../../../i18n";

export interface BrainQualityWidgetProps {
  snapshot?: AutonomousProjectSnapshot | null;
  onViewStudio?: () => void;
  className?: string;
}

export function BrainQualityWidget({
  snapshot,
  onViewStudio,
  className,
}: BrainQualityWidgetProps) {
  const { t } = useT("projects");

  const brain = snapshot?.brain;
  const qualityGates = snapshot?.quality_gates;
  const totalGatesCount = qualityGates?.length ?? 0;
  const passedGatesCount = useMemo(
    () =>
      (qualityGates ?? []).filter(
        (g) => g.status === "passed" || g.status === "success",
      ).length,
    [qualityGates],
  );

  const learningMode = brain?.learning_mode || "adaptive";

  return (
    <div
      className={cn(
        "flex flex-col rounded-xl border bg-card p-4 shadow-xs transition-colors",
        className,
      )}
    >
      {/* Header */}
      <div className="flex items-center justify-between gap-2 pb-3">
        <div className="flex items-center gap-2">
          <div className="flex size-7 items-center justify-center rounded-lg bg-purple-500/10 text-purple-600 dark:text-purple-400">
            <Brain className="size-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold leading-none text-foreground">
              {t(($) => $.cockpit.brain_quality_title)}
            </h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t(($) => $.cockpit.brain_quality_desc)}
            </p>
          </div>
        </div>

        {onViewStudio && (
          <Button
            variant="ghost"
            size="sm"
            className="gap-1 text-xs text-primary h-7 px-2"
            onClick={onViewStudio}
          >
            <span>Studio</span>
            <ArrowRight className="size-3" />
          </Button>
        )}
      </div>

      {/* Body */}
      <div className="flex-1 flex flex-col justify-between space-y-3">
        <div className="grid grid-cols-2 gap-2">
          {/* Active Memories */}
          <div className="rounded-lg bg-muted/20 border border-border/40 p-2.5">
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <BookOpen className="size-3 text-purple-500" />
              <span className="text-[10px] font-medium uppercase tracking-wider">
                {t(($) => $.cockpit.active_memories)}
              </span>
            </div>
            <div className="mt-1.5 flex items-baseline gap-1.5">
              <span className="font-mono text-base font-bold text-foreground">
                {brain?.active_memories ?? 0}
              </span>
              {Boolean(brain?.superseded_memories) && (
                <span className="text-[10px] text-muted-foreground">
                  (+{brain?.superseded_memories} archived)
                </span>
              )}
            </div>
          </div>

          {/* Quality Gate Pass Rate */}
          <div className="rounded-lg bg-muted/20 border border-border/40 p-2.5">
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <ShieldCheck className="size-3 text-emerald-500" />
              <span className="text-[10px] font-medium uppercase tracking-wider">
                Quality Gates
              </span>
            </div>
            <div className="mt-1.5 flex items-baseline gap-1.5">
              <span className="font-mono text-base font-bold text-emerald-600 dark:text-emerald-400">
                {totalGatesCount > 0
                  ? `${passedGatesCount} / ${totalGatesCount} (${Math.round((passedGatesCount / totalGatesCount) * 100)}%)`
                  : "100%"}
              </span>
              <span className="text-[10px] text-emerald-600/80">passed</span>
            </div>
          </div>
        </div>

        {/* Learning Mode & Specification Badge */}
        <div className="rounded-lg bg-muted/10 border border-border/40 p-2.5 space-y-2">
          <div className="flex items-center justify-between text-[11px]">
            <span className="text-muted-foreground">{t(($) => $.cockpit.learning_mode)}</span>
            <Badge variant="outline" className="text-[10px] font-mono capitalize">
              {learningMode}
            </Badge>
          </div>

          <div className="flex items-center justify-between text-[11px] pt-1 border-t border-border/30">
            <span className="text-muted-foreground">Specification Constraints</span>
            <span className="font-mono text-foreground text-[10px]">
              {snapshot?.plan?.specification?.requirements?.length ?? 0} requirements tracked
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
