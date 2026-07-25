FROM node:24-alpine AS build

WORKDIR /workspace
RUN apk add --no-cache bash
RUN corepack enable && corepack prepare pnpm@11.12.0 --activate

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json ./
COPY apps/web/package.json apps/web/package.json
COPY packages/runtime/package.json packages/runtime/package.json
COPY features/f02-marketing-site/scripts/link-workspace-runtime.mjs features/f02-marketing-site/scripts/link-workspace-runtime.mjs
RUN pnpm install --frozen-lockfile

COPY apps/web apps/web
COPY packages/runtime packages/runtime
COPY services/api/features services/api/features
COPY features features
COPY scripts/runtime scripts/runtime
RUN pnpm --filter @postqron/web build \
    && ./scripts/runtime/bundle-features.sh features /out/features

FROM node:24-alpine AS runtime

ENV HOST=0.0.0.0
ENV PORT=3000
ENV NODE_ENV=production
WORKDIR /app
USER node

COPY --from=build --chown=node:node /workspace/apps/web/.output ./
COPY --from=build --chown=node:node /out/features ./features
COPY --from=build --chown=node:node /workspace/apps/web/features ./foundation/web
COPY --chown=node:node services/api/features ./foundation/api

EXPOSE 3000
ENV POSTQRON_FEATURE_ROOTS=/app/foundation/web:/app/foundation/api:/app/features
CMD ["node", "server/index.mjs"]
