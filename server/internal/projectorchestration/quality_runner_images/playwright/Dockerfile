FROM mcr.microsoft.com/playwright:v1.56.1-noble@sha256:f1e7e01021efd65dd1a2c56064be399f3e4de00fd021ac561325f2bfbb2b837a

ENV COREPACK_HOME=/opt/corepack
ENV COREPACK_ENABLE_NETWORK=0
ENV COREPACK_DEFAULT_TO_LATEST=0
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright

RUN npm install --global --omit=dev playwright@1.56.1 corepack@0.34.0 \
    && mkdir -p /opt/corepack /opt/multica \
    && COREPACK_ENABLE_NETWORK=1 COREPACK_HOME=/opt/corepack corepack prepare pnpm@10.28.2 --activate \
    && test "$(node -p "require('/usr/lib/node_modules/playwright/package.json').version")" = "1.56.1" \
    && test "$(COREPACK_HOME=/opt/corepack corepack pnpm --version)" = "10.28.2" \
    && node -e "const fs=require('node:fs');const {chromium}=require('/usr/lib/node_modules/playwright');if(!fs.existsSync(chromium.executablePath()))process.exit(1)"

COPY qa-runner.cjs /opt/multica/qa-runner.cjs

ENV PATH=/usr/local/bin:/usr/bin:/bin
