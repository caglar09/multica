// @vitest-environment node
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Stats } from "fs";

vi.mock("electron", () => ({
  ipcMain: { handle: vi.fn() },
  dialog: { showOpenDialog: vi.fn() },
  BrowserWindow: { fromWebContents: vi.fn() },
  shell: { openPath: vi.fn() },
}));

vi.mock("fs/promises", () => ({
  access: vi.fn(),
  stat: vi.fn(),
}));

import { stat } from "fs/promises";
import { shell } from "electron";
import { openLocalDirectory } from "./local-directory";

function fakeStats(isDirectory: boolean): Stats {
  return { isDirectory: () => isDirectory } as unknown as Stats;
}

describe("openLocalDirectory", () => {
  beforeEach(() => {
    vi.mocked(stat).mockReset();
    vi.mocked(shell.openPath).mockReset();
    vi.mocked(shell.openPath).mockResolvedValue("");
  });

  it("rejects non-absolute input before reaching the OS shell", async () => {
    await expect(openLocalDirectory("../project")).resolves.toEqual({
      ok: false,
      reason: "not_absolute",
    });
    expect(stat).not.toHaveBeenCalled();
    expect(shell.openPath).not.toHaveBeenCalled();
  });

  it("rejects files so the bridge cannot launch arbitrary documents", async () => {
    vi.mocked(stat).mockResolvedValue(fakeStats(false));

    await expect(openLocalDirectory("/tmp/project.txt")).resolves.toEqual({
      ok: false,
      reason: "not_a_directory",
    });
    expect(shell.openPath).not.toHaveBeenCalled();
  });

  it("reports a missing directory without calling shell.openPath", async () => {
    vi.mocked(stat).mockRejectedValue(
      Object.assign(new Error("missing"), { code: "ENOENT" }),
    );

    await expect(openLocalDirectory("/tmp/missing-project")).resolves.toEqual({
      ok: false,
      reason: "not_found",
    });
    expect(shell.openPath).not.toHaveBeenCalled();
  });

  it("opens an existing directory in the system file manager", async () => {
    vi.mocked(stat).mockResolvedValue(fakeStats(true));

    await expect(openLocalDirectory("/tmp/project")).resolves.toEqual({
      ok: true,
    });
    expect(shell.openPath).toHaveBeenCalledWith("/tmp/project");
  });

  it("surfaces the OS shell error when the directory cannot be opened", async () => {
    vi.mocked(stat).mockResolvedValue(fakeStats(true));
    vi.mocked(shell.openPath).mockResolvedValue("file manager unavailable");

    await expect(openLocalDirectory("/tmp/project")).resolves.toEqual({
      ok: false,
      reason: "error",
      error: "file manager unavailable",
    });
  });
});
