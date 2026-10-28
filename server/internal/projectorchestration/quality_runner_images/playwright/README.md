# Playwright browser quality runner

Build this image locally from this directory. The build context contains only
the pinned runner definition and its fixed browser script; project source is
never copied into the image.

```sh
docker build --pull=false \
  --tag multica-quality-playwright:1.56.1-pnpm-10.28.2 \
  --file Dockerfile .
```

The Playwright base image is pinned by version and manifest digest. The build
installs Playwright 1.56.1 and pnpm 10.28.2 and verifies Chromium is present.
At runtime the daemon resolves this image locally, inspects its immutable image
ID, disables image pulls, and runs it with `--network=none`, a read-only root
filesystem, dropped capabilities, and bounded CPU, memory, process, and tmpfs
limits. If the image, Playwright, or Chromium is unavailable, the gate fails
closed.

For acceptance gates, provide `artifact.browser_qa` with a loopback `target_url`,
a `preview_command` argument array, selector-based `steps`, and an optional
`viewport` and `capture_screenshots` flag. The preview command runs inside the
same networkless container from the archived task commit. The browser blocks
requests outside the configured origin. Network access stays disabled, so
uncached project dependencies cannot be fetched; a missing preview dependency
or browser produces a failed gate. No host process, worktree, or host network
is mounted or reachable.

Supported steps are `click <selector>`, `fill <selector> = <value>`,
`assert-visible <selector>`, `assert-hidden <selector>`,
`assert-text <selector> = <expected text>`, and `assert-url </path?query>`.
The runner captures console errors, failed or blocked requests, an execution
trace, and bounded JPEG screenshots. Screenshot bytes and their SHA-256 digests
are included in the quality gate evidence, so the files remain reviewable after
the temporary container is removed.
