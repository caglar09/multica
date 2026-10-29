import { afterEach, describe, expect, it, vi } from "vitest";
import { openLocalDirectory, prepareLocalDirectory } from "./local-directory";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("openLocalDirectory web daemon routing", () => {
  it("opens through the project daemon's non-default health port", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ daemon_id: "daemon-1" }), { status: 200 }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      openLocalDirectory("/tmp/project", {
        daemonId: "daemon-1",
        healthPort: 20387,
      }),
    ).resolves.toEqual({ ok: true });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "http://127.0.0.1:20387/health",
      { cache: "no-store" },
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "http://127.0.0.1:20387/open-directory",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("does not open a folder when the daemon identity does not match", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ daemon_id: "other-daemon" }), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await openLocalDirectory("/tmp/project", {
      daemonId: "daemon-1",
      healthPort: 20387,
    });

    expect(result.ok).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("prepareLocalDirectory web daemon routing", () => {
	it("checks the local daemon and initializes Git only after confirmation", async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify({ daemon_id: "daemon-1" }), { status: 200 }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ ok: true, daemon_id: "daemon-1", is_git_repo: true, initialized_git: true }), { status: 200 }));
		vi.stubGlobal("fetch", fetchMock);

		await expect(prepareLocalDirectory("/Users/example/project", { daemonId: "daemon-1", initializeGit: true })).resolves.toEqual({
			ok: true,
			daemon_id: "daemon-1",
			is_git_repo: true,
			initialized_git: true,
		});
		expect(fetchMock).toHaveBeenNthCalledWith(2, "http://127.0.0.1:19514/project-directory/prepare", expect.objectContaining({
			method: "POST",
			body: JSON.stringify({ path: "/Users/example/project", initialize_git: true }),
		}));
	});

	it("does not prepare a directory on a different daemon", async () => {
		const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ daemon_id: "other-daemon" }), { status: 200 }));
		vi.stubGlobal("fetch", fetchMock);

		const result = await prepareLocalDirectory("/Users/example/project", { daemonId: "daemon-1" });
		expect(result.ok).toBe(false);
		expect(fetchMock).toHaveBeenCalledTimes(1);
	});
});
