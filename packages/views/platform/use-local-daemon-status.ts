"use client";

import { useEffect, useState } from "react";

const LOCAL_DAEMON_HEALTH_URL = "http://127.0.0.1:19514/health";

/** Subset of the daemonAPI status shape that the local_directory UI consumes.
 *  Redeclared here so this hook doesn't depend on the desktop preload types. */
export interface LocalDaemonStatus {
  daemonId: string | null;
  deviceName: string | null;
  running: boolean;
}

interface DaemonStatusLike {
  state:
    | "running"
    | "stopped"
    | "starting"
    | "stopping"
    | "installing_cli"
    | "cli_not_found"
    | "auth_expired";
  daemonId?: string;
  deviceName?: string;
}

interface DaemonAPILike {
  getStatus?: () => Promise<DaemonStatusLike>;
  onStatusChange?: (cb: (s: DaemonStatusLike) => void) => () => void;
}

function readDaemonAPI(): DaemonAPILike | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as { daemonAPI?: DaemonAPILike }).daemonAPI;
}

function toStatus(s: DaemonStatusLike | undefined): LocalDaemonStatus {
  if (!s) return { daemonId: null, deviceName: null, running: false };
  return {
    daemonId: s.daemonId ?? null,
    deviceName: s.deviceName ?? null,
    running: s.state === "running",
  };
}

/**
 * Live snapshot of the desktop's local daemon: the daemon_id it registers
 * under, the OS device name, and whether the supervisor is currently running.
 *
 * On web (no `window.daemonAPI`) every field is null/false — components can
 * unconditionally call this hook and branch on `daemonId` to decide whether
 * a local_directory resource matches "this machine".
 *
 * The initial paint reads `getStatus()` once so the UI doesn't flash a
 * "no daemon" state while waiting for the first push from `onStatusChange`.
 */
export function useLocalDaemonStatus(): LocalDaemonStatus {
  const [status, setStatus] = useState<LocalDaemonStatus>(() => ({
    daemonId: null,
    deviceName: null,
    running: false,
  }));

  useEffect(() => {
    const api = readDaemonAPI();
    let cancelled = false;

    if (api) {
      if (api.getStatus) {
        api.getStatus().then((s) => {
          if (!cancelled) setStatus(toStatus(s));
        }).catch(() => {
          // Ignore — onStatusChange will populate once the daemon comes up.
        });
      }
      const unsubscribe = api.onStatusChange?.((s) => {
        setStatus(toStatus(s));
      });
      return () => {
        cancelled = true;
        unsubscribe?.();
      };
    }

    // The web app can still be running on the same machine as the daemon
    // (the common self-host/dev setup). Probe the daemon's loopback-only health
    // endpoint so project detail can match daemon-owned absolute paths even
    // outside Electron. Remote deployments simply fail this fetch and keep the
    // existing null/offline fallback.
    fetch(LOCAL_DAEMON_HEALTH_URL, { cache: "no-store" })
      .then(async (response) => {
        if (!response.ok) throw new Error("daemon health unavailable");
        return response.json() as Promise<{
          status?: string;
          daemon_id?: string;
          device_name?: string;
        }>;
      })
      .then((health) => {
        if (cancelled) return;
        setStatus({
          daemonId: health.daemon_id ?? null,
          deviceName: health.device_name ?? null,
          running: health.status === "running",
        });
      })
      .catch(() => {
        // No local daemon reachable from this browser. Keep the safe fallback.
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return status;
}
