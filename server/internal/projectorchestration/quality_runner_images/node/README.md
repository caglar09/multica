# Node quality runner image

Build the local image from this directory so the build context contains only
this Dockerfile and README, never project source:

```sh
docker build --pull=false \
  --tag multica-quality-node:22.23.4-pnpm-10.28.2 \
  --file Dockerfile .
```

The base is the locally pinned `node:22.23.4-alpine` image digest in the
Dockerfile. The build preloads Corepack's pnpm 10.28.2 package; that build step
needs access to the npm registry unless the builder already has the package.
The quality runner itself only resolves this image locally, runs it by its
immutable image ID, disables pulls and container networking, and fails closed
when the image or pnpm preflight is unavailable.

The project worktree is mounted into a temporary networkless container only
as a read-only Git archive of the recorded commit. Dependencies are not copied
into this image or mounted from the host, so repository commands that require
uncached packages may remain unavailable offline.
