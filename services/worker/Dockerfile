FROM golang:1.26-alpine AS build

WORKDIR /workspace
RUN apk add --no-cache bash
COPY go.work ./
COPY packages/runtime/go/go.mod packages/runtime/go/go.sum packages/runtime/go/
COPY features/f03-auth/go.mod features/f03-auth/go.sum features/f03-auth/
COPY features/f04-workspaces/go.mod features/f04-workspaces/go.sum features/f04-workspaces/
COPY features/f05-social-connections/go.mod features/f05-social-connections/go.sum features/f05-social-connections/
COPY features/f08-publishing/go.mod features/f08-publishing/go.sum features/f08-publishing/
COPY features/f09-status-notifications/go.mod features/f09-status-notifications/go.sum features/f09-status-notifications/
COPY features/f14-email/go.mod features/f14-email/
COPY services/api/go.mod services/api/go.sum services/api/
COPY services/worker/go.mod services/worker/go.sum services/worker/
RUN GOWORK=off go mod download -C services/worker

COPY packages/runtime/go packages/runtime/go
COPY services/worker services/worker
COPY features features
COPY scripts/runtime scripts/runtime
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./services/worker/cmd/worker \
    && ./scripts/runtime/bundle-features.sh features /out/features

FROM alpine:3.23 AS runtime

RUN addgroup -S postqron \
    && adduser -S -G postqron postqron \
    && mkdir -p /var/lib/postqron/privacy-exports \
    && chown -R postqron:postqron /var/lib/postqron \
    && chmod 0700 /var/lib/postqron/privacy-exports
WORKDIR /app
COPY --from=build /out/worker /usr/local/bin/worker
COPY --from=build /out/features ./features
COPY features/f01-brand/tokens ./features/f01-brand/tokens
COPY services/worker/features ./foundation/worker
COPY services/api/features ./foundation/api

USER postqron
ENV POSTQRON_FEATURE_ROOTS=/app/foundation/worker:/app/foundation/api:/app/features
CMD ["worker"]
