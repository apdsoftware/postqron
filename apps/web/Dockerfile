FROM node:24-alpine AS build

WORKDIR /workspace
RUN corepack enable && corepack prepare pnpm@11.12.0 --activate

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json ./
COPY apps/web/package.json apps/web/package.json
COPY packages/runtime/package.json packages/runtime/package.json
RUN pnpm install --frozen-lockfile

COPY apps/web apps/web
COPY packages/runtime packages/runtime
RUN pnpm --filter @postqron/web build

FROM node:24-alpine AS runtime

ENV HOST=0.0.0.0
ENV PORT=3000
ENV NODE_ENV=production
WORKDIR /app
USER node

COPY --from=build --chown=node:node /workspace/apps/web/.output ./
COPY --from=build --chown=node:node /workspace/apps/web/features ./features

EXPOSE 3000
ENV POSTQRON_FEATURE_ROOTS=/app/features
CMD ["node", "server/index.mjs"]
