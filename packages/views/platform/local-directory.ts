// Desktop-only helpers for the project_resource local_directory flow.
//
// These wrap the preload `desktopAPI` surface so view components can
// SSR-render on web (where `window.desktopAPI` is undefined) and degrade
// gracefully to no-op promises instead of crashing.

const LOCAL_DAEMON_OPEN_DIRECTORY_URL = "http://127.0.0.1:19514/open-directory";

export type PickDirectoryResult = {
  ok: boolean;
  path?: string;
  basename?: string;
  reason?: "cancelled" | "no_window" | "error" | "unsupported";
  error?: string;
};

export type ValidateLocalDirectoryResult = {
  ok: boolean;
  reason?:
    | "not_absolute"
    | "not_found"
    | "not_a_directory"
    | "not_readable"
    | "not_writable"
    | "error"
    | "unsupported";
  error?: string;
  /**
   * Whether the directory sits inside a git working tree. Only meaningful when
   * ok=true; absent from an older desktop build, which is why callers must
   * treat `undefined` as "unknown" rather than "not a repo".
   */
  is_git_repo?: boolean;
};

export type OpenLocalDirectoryResult = {
  ok: boolean;
  reason?:
    | "not_absolute"
    | "not_found"
    | "not_a_directory"
    | "error"
    | "unsupported";
  error?: string;
};

interface DesktopLocalDirectoryAPI {
  pickDirectory?: (defaultPath?: string) => Promise<PickDirectoryResult>;
  validateLocalDirectory?: (
    path: string,
  ) => Promise<ValidateLocalDirectoryResult>;
  openLocalDirectory?: (path: string) => Promise<OpenLocalDirectoryResult>;
}

function readDesktopAPI(): DesktopLocalDirectoryAPI | undefined {
  if (typeof window === "undefined") return undefined;
  const api = (window as unknown as { desktopAPI?: DesktopLocalDirectoryAPI })
    .desktopAPI;
  return api;
}

/** True when the renderer is running inside the Electron desktop shell, as
 *  evidenced by the preload-exposed pickDirectory bridge. Avoids hard-coding
 *  navigator/process checks — those vary across electron-vite + jsdom tests. */
export function isDesktopShell(): boolean {
  const api = readDesktopAPI();
  return typeof api?.pickDirectory === "function";
}

export async function pickDirectory(
  defaultPath?: string,
): Promise<PickDirectoryResult> {
  const api = readDesktopAPI();
  if (!api?.pickDirectory) return { ok: false, reason: "unsupported" };
  return api.pickDirectory(defaultPath);
}

export async function validateLocalDirectory(
  path: string,
): Promise<ValidateLocalDirectoryResult> {
  const api = readDesktopAPI();
  if (!api?.validateLocalDirectory) return { ok: false, reason: "unsupported" };
  return api.validateLocalDirectory(path);
}

export async function openLocalDirectory(
  path: string,
): Promise<OpenLocalDirectoryResult> {
  const api = readDesktopAPI();
  if (api?.openLocalDirectory) return api.openLocalDirectory(path);

  // Local/self-host web UI: ask the loopback daemon to reveal only a
  // daemon-managed workspace directory. The daemon enforces both loopback
  // Origin and WorkspacesRoot containment, so this does not become a generic
  // browser-to-filesystem bridge.
  if (typeof window !== "undefined") {
    try {
      const response = await fetch(LOCAL_DAEMON_OPEN_DIRECTORY_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path }),
      });
      if (response.ok) return { ok: true };
      const message = (await response.text()).trim();
      return {
        ok: false,
        reason: "error",
        error: message || `Failed to open directory (HTTP ${response.status})`,
      };
    } catch (error) {
      return {
        ok: false,
        reason: "unsupported",
        error: error instanceof Error ? error.message : String(error),
      };
    }
  }

  return { ok: false, reason: "unsupported" };
}
