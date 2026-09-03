"use client";

import { useState } from "react";
import { Download, RefreshCw, ScrollText, ShieldAlert } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import type { DiagnosticLogEntry, DiagnosticLogQuery } from "@multica/core/types";
import { useDiagnosticsLogsEnabled } from "@multica/core/diagnostics";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import { cn } from "@multica/ui/lib/utils";
import { CollectionPageHeader, CollectionPageState } from "../layout/collection-page";
import { PAGE_GUTTER } from "../layout/page-header";

const TAIL_OPTIONS = [200, 1000, 5000] as const;
const WINDOW_OPTIONS = [
  { label: "All available", seconds: 0 },
  { label: "Last 5 min", seconds: 300 },
  { label: "Last hour", seconds: 3600 },
  { label: "Last 6 hours", seconds: 21600 },
  { label: "Last 24 hours", seconds: 86400 },
] as const;

function severityClass(level: DiagnosticLogEntry["level"]) {
  switch (level) {
    case "error":
      return "text-destructive";
    case "warn":
      return "text-warning";
    case "debug":
      return "text-muted-foreground";
    default:
      return "text-foreground";
  }
}

export function LogsPage() {
  const enabled = useDiagnosticsLogsEnabled();
  const [source, setSource] = useState("");
  const [search, setSearch] = useState("");
  const [tail, setTail] = useState<number>(1000);
  const [windowSeconds, setWindowSeconds] = useState(3600);
  const [live, setLive] = useState(true);

  const buildQuery = (exportMode = false): DiagnosticLogQuery => ({
    ...(source ? { source } : {}),
    ...(search.trim() ? { search: search.trim() } : {}),
    tail: exportMode ? 50000 : tail,
    ...(windowSeconds > 0
      ? { since: Math.floor(Date.now() / 1000) - windowSeconds }
      : {}),
  });

  const logs = useQuery({
    queryKey: ["diagnostics", "logs", source, search, tail, windowSeconds],
    queryFn: () => api.getDiagnosticLogs(buildQuery()),
    enabled,
    refetchInterval: live ? 2500 : false,
    refetchIntervalInBackground: false,
  });

  if (!enabled) {
    return (
      <CollectionPageState
        icon={ShieldAlert}
        title="Logs experiment is disabled"
        description="Enable Logs in Settings → Labs to open deployment diagnostics."
      />
    );
  }

  const exportLogs = async () => {
    const blob = await api.exportDiagnosticLogs(buildQuery(true));
    const href = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = href;
    a.download =
      "multica-logs-" + new Date().toISOString().replace(/[:.]/g, "-") + ".ndjson";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(href);
  };

  const error = logs.error instanceof ApiError ? logs.error : null;
  const entries = logs.data?.entries ?? [];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <CollectionPageHeader
        icon={ScrollText}
        title="Logs"
        count={entries.length}
        description="Backend, PostgreSQL, frontend and other Multica service logs."
        actions={
          <>
            <Button
              size="sm"
              variant="outline"
              onClick={() => logs.refetch()}
              disabled={logs.isFetching}
            >
              <RefreshCw
                className={cn("size-3.5", logs.isFetching && "animate-spin")}
              />
              <span className="hidden md:inline">Refresh</span>
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={exportLogs}
              disabled={logs.isPending || !!error}
            >
              <Download className="size-3.5" />
              <span className="hidden md:inline">Export</span>
            </Button>
          </>
        }
      />

      <div className={cn(PAGE_GUTTER, "flex min-h-0 flex-1 flex-col gap-3 py-3")}>
        <div className="flex flex-wrap items-center gap-2">
          <select
            aria-label="Log source"
            className="h-8 rounded-md border bg-background px-2 text-body"
            value={source}
            onChange={(e) => setSource(e.target.value)}
          >
            <option value="">All services</option>
            {(logs.data?.sources ?? []).map((item) => (
              <option key={item.id} value={item.id}>
                {item.label}
              </option>
            ))}
          </select>
          <select
            aria-label="Time window"
            className="h-8 rounded-md border bg-background px-2 text-body"
            value={windowSeconds}
            onChange={(e) => setWindowSeconds(Number(e.target.value))}
          >
            {WINDOW_OPTIONS.map((item) => (
              <option key={item.seconds} value={item.seconds}>
                {item.label}
              </option>
            ))}
          </select>
          <select
            aria-label="Tail lines"
            className="h-8 rounded-md border bg-background px-2 text-body"
            value={tail}
            onChange={(e) => setTail(Number(e.target.value))}
          >
            {TAIL_OPTIONS.map((value) => (
              <option key={value} value={value}>
                {value} lines/service
              </option>
            ))}
          </select>
          <Input
            className="h-8 min-w-52 flex-1 md:max-w-sm"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search logs…"
          />
          <label className="ml-auto flex items-center gap-2 text-caption text-muted-foreground">
            Live
            <Switch checked={live} onCheckedChange={setLive} />
          </label>
        </div>

        {error ? (
          <CollectionPageState
            icon={ShieldAlert}
            title={error.status === 403 ? "Admin access required" : "Logs unavailable"}
            description={
              error.status === 403
                ? "Raw deployment logs are restricted to workspace owners and admins."
                : error.message
            }
            className="flex-1"
          />
        ) : (
          <div className="min-h-0 flex-1 overflow-auto rounded-md border bg-background">
            {entries.length === 0 && !logs.isPending ? (
              <div className="p-6 text-center text-body text-muted-foreground">
                No log lines match the current filters.
              </div>
            ) : (
              <div className="min-w-max py-1 font-mono text-[12px] leading-5">
                {entries.map((entry, index) => (
                  <div
                    key={[
                      entry.timestamp ?? "none",
                      entry.source,
                      String(index),
                    ].join("-")}
                    className="grid grid-cols-[190px_110px_70px_minmax(500px,1fr)] gap-2 border-b border-border/40 px-3 py-0.5 hover:bg-muted/30"
                  >
                    <span className="text-muted-foreground">
                      {entry.timestamp
                        ? new Date(entry.timestamp).toLocaleString()
                        : "—"}
                    </span>
                    <span className="font-semibold">{entry.source}</span>
                    <span className={severityClass(entry.level)}>
                      {entry.level.toUpperCase()}
                    </span>
                    <span
                      className={cn(
                        "whitespace-pre-wrap break-words",
                        severityClass(entry.level),
                      )}
                    >
                      {entry.message}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        <div className="flex items-center justify-between text-caption text-muted-foreground">
          <span>
            {logs.data?.sources
              .map(
                (item) =>
                  item.label + ": " + (item.state || "unknown"),
              )
              .join(" · ")}
          </span>
          <span>
            {logs.data?.collected_at
              ? "Collected " +
                new Date(logs.data.collected_at).toLocaleTimeString()
              : ""}
          </span>
        </div>
      </div>
    </div>
  );
}
