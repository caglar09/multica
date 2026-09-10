/* eslint-disable i18next/no-literal-string */
"use client";

import { useMemo } from "react";
import { Gauge, Coins, Clock, ArrowRight, Zap } from "lucide-react";

import type { AutonomousBudget } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";

import { useT } from "../../../../i18n";

export interface TelemetryCostWidgetProps {
  budget?: AutonomousBudget | null;
  onViewReports?: () => void;
  className?: string;
}

function formatTokens(value: number): string {
  return new Intl.NumberFormat(undefined, {
    notation: value >= 10_000 ? "compact" : "standard",
    maximumFractionDigits: 1,
  }).format(value);
}

function formatCostMicros(value: number): string {
  const dollars = value / 1_000_000;
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  }).format(dollars);
}

function formatDurationSeconds(seconds: number): string {
  if (!seconds || seconds <= 0) return "0s";
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const mins = Math.floor(seconds / 60);
  const restSecs = Math.round(seconds % 60);
  if (mins < 60) return `${mins}m ${restSecs}s`;
  const hours = Math.floor(mins / 60);
  return `${hours}h ${mins % 60}m`;
}

export function TelemetryCostWidget({
  budget,
  onViewReports,
  className,
}: TelemetryCostWidgetProps) {
  const { t } = useT("projects");

  const tokensUsed = budget?.tokens_used ?? 0;
  const tokenLimit = budget?.token_limit ?? null;
  const costUsed = budget?.cost_microunits_used ?? 0;
  const runtimeSeconds = budget?.runtime_seconds_used ?? 0;

  const tokenPercent = useMemo(() => {
    if (!tokenLimit || tokenLimit <= 0) return 0;
    return Math.min(100, Math.round((tokensUsed / tokenLimit) * 100));
  }, [tokenLimit, tokensUsed]);

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
          <div className="flex size-7 items-center justify-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <Gauge className="size-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold leading-none text-foreground">
              {t(($) => $.cockpit.telemetry_title)}
            </h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t(($) => $.cockpit.telemetry_desc)}
            </p>
          </div>
        </div>

        {onViewReports && (
          <Button
            variant="ghost"
            size="sm"
            className="gap-1 text-xs text-primary h-7 px-2"
            onClick={onViewReports}
          >
            <span>Analytics</span>
            <ArrowRight className="size-3" />
          </Button>
        )}
      </div>

      {/* Body: Telemetry KPI Cards */}
      <div className="flex-1 flex flex-col justify-between space-y-3">
        <div className="grid grid-cols-3 gap-2">
          {/* Tokens Used */}
          <div className="rounded-lg bg-muted/20 border border-border/40 p-2.5">
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <Zap className="size-3 text-amber-500" />
              <span className="text-[10px] font-medium uppercase tracking-wider">
                {t(($) => $.cockpit.tokens_metric)}
              </span>
            </div>
            <span className="mt-1.5 block font-mono text-base font-bold text-foreground">
              {formatTokens(tokensUsed)}
            </span>
          </div>

          {/* Cost Used */}
          <div className="rounded-lg bg-muted/20 border border-border/40 p-2.5">
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <Coins className="size-3 text-emerald-500" />
              <span className="text-[10px] font-medium uppercase tracking-wider">
                {t(($) => $.cockpit.cost_metric)}
              </span>
            </div>
            <span className="mt-1.5 block font-mono text-base font-bold text-emerald-600 dark:text-emerald-400">
              {formatCostMicros(costUsed)}
            </span>
          </div>

          {/* Active Runtime */}
          <div className="rounded-lg bg-muted/20 border border-border/40 p-2.5">
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <Clock className="size-3 text-sky-500" />
              <span className="text-[10px] font-medium uppercase tracking-wider">
                {t(($) => $.cockpit.runtime_metric)}
              </span>
            </div>
            <span className="mt-1.5 block font-mono text-base font-bold text-foreground">
              {formatDurationSeconds(runtimeSeconds)}
            </span>
          </div>
        </div>

        {/* Token Budget Gauge */}
        {tokenLimit ? (
          <div className="rounded-lg bg-muted/10 border border-border/40 p-2.5">
            <div className="flex items-center justify-between text-[11px] mb-1.5">
              <span className="text-muted-foreground">Token Budget Allocation</span>
              <span className="font-mono font-medium text-foreground">
                {formatTokens(tokensUsed)} / {formatTokens(tokenLimit)} ({tokenPercent}%)
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn(
                  "h-full rounded-full transition-all duration-500",
                  tokenPercent > 85
                    ? "bg-destructive"
                    : tokenPercent > 60
                      ? "bg-amber-500"
                      : "bg-primary",
                )}
                style={{ width: `${tokenPercent}%` }}
              />
            </div>
          </div>
        ) : (
          <div className="flex items-center justify-between rounded-lg bg-muted/10 border border-border/40 px-3 py-2 text-[11px] text-muted-foreground">
            <span>Budget Control</span>
            <span className="font-mono text-foreground font-medium">Standard Consumption</span>
          </div>
        )}
      </div>
    </div>
  );
}
