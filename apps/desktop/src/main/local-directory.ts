import { ipcMain, dialog, BrowserWindow, shell } from "electron";
import { access, stat } from "fs/promises";
import { constants as fsConstants } from "fs";
import { basename, dirname, isAbsolute, join } from "path";

export interface PickDirectoryResult {
  ok: boolean;
  path?: string;
  basename?: string;
  /** Set when ok=false. "cancelled" = user dismissed; otherwise an error blurb. */
  reason?: "cancelled" | "no_window" | "error";
  error?: string;
}

export interface ValidateLocalDirectoryResult {
  ok: boolean;
  /** When ok=false, identifies which check failed so the renderer can render a
   *  specific message without parsing free-form text. */
  reason?:
    | "not_absolute"
    | "not_found"
    | "not_a_directory"
    | "not_readable"
    | "not_writable"
    | "error";
  error?: string;
  /**
   * Whether the directory sits inside a git working tree. Only set when ok=true.
   *
   * Worktree execution mode requires a git repo, and only the desktop app can
   * see the filesystem — the server cannot. Reporting it here lets the picker
   * disable that mode with a reason at selection time, instead of letting the
   * user save a resource whose very first task fails.
   */
  is_git_repo?: boolean;
}

export interface OpenLocalDirectoryResult {
  ok: boolean;
  reason?: "not_absolute" | "not_found" | "not_a_directory" | "error";
  error?: string;
}

async function validateLocalDirectory(
  path: string,
): Promise<ValidateLocalDirectoryResult> {
  if (!path || !isAbsolute(path)) {
    return { ok: false, reason: "not_absolute" };
  }
  try {
    const st = await stat(path);
    if (!st.isDirectory()) return { ok: false, reason: "not_a_directory" };
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code === "ENOENT") return { ok: false, reason: "not_found" };
    return { ok: false, reason: "error", error: errorMessage(err) };
  }
  try {
    await access(path, fsConstants.R_OK);
  } catch {
    return { ok: false, reason: "not_readable" };
  }
  try {
    await access(path, fsConstants.W_OK);
  } catch {
    return { ok: false, reason: "not_writable" };
  }
  return { ok: true, is_git_repo: await isInsideGitWorkTree(path) };
}

/**
 * Open a renderer-supplied directory in the operating system's file manager.
 *
 * The renderer may only ask us to open existing directories. In particular,
 * files are rejected before reaching `shell.openPath` so this IPC surface
 * cannot be repurposed to launch arbitrary executables or documents.
 */
export async function openLocalDirectory(
  path: unknown,
): Promise<OpenLocalDirectoryResult> {
  if (typeof path !== "string" || !path || !isAbsolute(path)) {
    return { ok: false, reason: "not_absolute" };
  }

  try {
    const st = await stat(path);
    if (!st.isDirectory()) return { ok: false, reason: "not_a_directory" };
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code === "ENOENT") return { ok: false, reason: "not_found" };
    return { ok: false, reason: "error", error: errorMessage(err) };
  }

  try {
    const error = await shell.openPath(path);
    if (error) return { ok: false, reason: "error", error };
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: "error", error: errorMessage(err) };
  }
}

/**
 * Walks up from `path` looking for a `.git` entry, mirroring how git itself
 * resolves a working tree — so a subdirectory of a repo reports true, matching
 * what the daemon does with `rev-parse --show-toplevel` at task time.
 *
 * `.git` is accepted as either a directory (ordinary clone) or a file (a linked
 * worktree, where it holds a gitdir pointer). Any error means "can't tell",
 * which is reported as not-a-repo: this only drives a UI hint, and the daemon
 * re-checks authoritatively before running anything.
 */
async function isInsideGitWorkTree(path: string): Promise<boolean> {
  let current = path;
  for (;;) {
    try {
      await stat(join(current, ".git"));
      return true;
    } catch {
      // Not here — keep walking up.
    }
    const parent = dirname(current);
    if (parent === current) return false;
    current = parent;
  }
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export function setupLocalDirectory(
  windowGetter: () => BrowserWindow | null,
): void {
  ipcMain.handle(
    "local-directory:pick",
    async (event, defaultPath?: string): Promise<PickDirectoryResult> => {
      const win =
        BrowserWindow.fromWebContents(event.sender) ?? windowGetter();
      if (!win) return { ok: false, reason: "no_window" };
      try {
        const result = await dialog.showOpenDialog(win, {
          // Multiple-selection is intentionally disabled — a project_resource
          // points at a single directory, and the create flow expects one
          // path per click. Multi-add would have to be a separate UX.
          properties: ["openDirectory", "createDirectory"],
          ...(defaultPath ? { defaultPath } : {}),
        });
        if (result.canceled || result.filePaths.length === 0) {
          return { ok: false, reason: "cancelled" };
        }
        const picked = result.filePaths[0];
        if (!picked) return { ok: false, reason: "cancelled" };
        return { ok: true, path: picked, basename: basename(picked) };
      } catch (err) {
        return { ok: false, reason: "error", error: errorMessage(err) };
      }
    },
  );

  ipcMain.handle(
    "local-directory:validate",
    (_event, path: string): Promise<ValidateLocalDirectoryResult> =>
      validateLocalDirectory(path),
  );

  ipcMain.handle(
    "local-directory:open",
    (_event, path: unknown): Promise<OpenLocalDirectoryResult> =>
      openLocalDirectory(path),
  );
}
