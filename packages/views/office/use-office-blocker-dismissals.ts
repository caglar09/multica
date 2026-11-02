"use client";

import { useCallback, useMemo, useSyncExternalStore } from "react";
import type { OfficeBlocker } from "./types";

const STORAGE_PREFIX = "multica.office.dismissed-blockers.";
const listeners = new Set<() => void>();

function storageKey(userId: string, projectId: string): string {
  return `${STORAGE_PREFIX}${userId}.${projectId}`;
}

function readDismissed(key: string | null): string {
  if (!key || typeof window === "undefined") return "";
  try {
    return window.localStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}

function subscribe(callback: () => void): () => void {
  listeners.add(callback);
  const onStorage = (event: StorageEvent) => {
    if (event.key?.startsWith(STORAGE_PREFIX)) callback();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(callback);
    window.removeEventListener("storage", onStorage);
  };
}

export function useOfficeBlockerDismissals(userId: string | null | undefined, projectId: string | null) {
  const key = userId && projectId ? storageKey(userId, projectId) : null;
  const raw = useSyncExternalStore(subscribe, () => readDismissed(key), () => "");
  const dismissedIds = useMemo(() => {
    try {
      const value: unknown = JSON.parse(raw);
      return new Set(Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : []);
    } catch {
      return new Set<string>();
    }
  }, [raw]);

  const dismiss = useCallback((blockers: readonly OfficeBlocker[]) => {
    if (!key) return;
    try {
      const stored = readDismissed(key);
      const previous: unknown = stored ? JSON.parse(stored) : [];
      const ids = new Set(Array.isArray(previous)
        ? previous.filter((id): id is string => typeof id === "string")
        : []);
      for (const blocker of blockers) ids.add(blocker.id);
      window.localStorage.setItem(key, JSON.stringify([...ids]));
      for (const listener of listeners) listener();
    } catch {
      // Keep local dismissal best-effort when browser storage is unavailable.
    }
  }, [key]);

  return [dismissedIds, dismiss] as const;
}
