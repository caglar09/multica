FROM node:22.23.4-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914

RUN mkdir -p /opt/corepack \
    && COREPACK_HOME=/opt/corepack corepack prepare pnpm@10.28.2 --activate \
    && test "$(COREPACK_HOME=/opt/corepack corepack pnpm --version)" = "10.28.2"

ENV COREPACK_HOME=/opt/corepack
ENV COREPACK_ENABLE_NETWORK=0
