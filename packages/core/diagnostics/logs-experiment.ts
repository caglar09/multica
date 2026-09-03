"use client";

import { useSyncExternalStore } from "react";

const STORAGE_KEY = "multica:labs:diagnostics-logs";
let cached: boolean | null = null;
const listeners = new Set<() => void>();

function read(): boolean {
  if (cached !== null) return cached;
  if (typeof window === "undefined") return false;
  cached = window.localStorage.getItem(STORAGE_KEY) === "true";
  return cached;
}

export function isDiagnosticsLogsEnabled(): boolean {
  return read();
}

export function setDiagnosticsLogsEnabled(enabled: boolean): void {
  cached = enabled;
  if (typeof window !== "undefined") {
    window.localStorage.setItem(STORAGE_KEY, enabled ? "true" : "false");
  }
  for (const listener of listeners) listener();
}

export function useDiagnosticsLogsEnabled(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    read,
    () => false,
  );
}
