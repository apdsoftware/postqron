FROM golang:1.26-alpine AS build

WORKDIR /workspace
RUN apk add --no-cache bash
COPY go.work ./
COPY packages/runtime/go/go.mod packages/runtime/go/go.sum packages/runtime/go/
COPY features/f03-auth/go.mod features/f03-auth/go.sum features/f03-auth/
COPY features/f04-workspaces/go.mod features/f04-workspaces/go.sum features/f04-workspaces/
COPY features/f05-social-connections/go.mod features/f05-social-connections/go.sum features/f05-social-connections/
COPY features/f07-scheduling/go.mod features/f07-scheduling/go.sum features/f07-scheduling/
COPY features/f06-composer/go.mod features/f06-composer/go.sum features/f06-composer/
COPY features/f12-account-privacy/go.mod features/f12-account-privacy/go.sum features/f12-account-privacy/
COPY features/f14-email/go.mod features/f14-email/
COPY features/f15-operations/go.mod features/f15-operations/
COPY features/f26-cookie-consent-api/go.mod features/f26-cookie-consent-api/go.sum features/f26-cookie-consent-api/
COPY features/f30-app-shell/go.mod features/f30-app-shell/
COPY features/f31-admin-console/go.mod features/f31-admin-console/
COPY features/f34-prelaunch/api/go.mod features/f34-prelaunch/api/
COPY services/api/go.mod services/api/go.sum services/api/
COPY services/worker/go.mod services/worker/go.sum services/worker/
COPY features/f10-entitlements/go.mod features/f10-entitlements/go.sum features/f10-entitlements/
RUN GOWORK=off go mod download -C services/api

COPY packages/runtime/go packages/runtime/go
COPY services/api services/api
COPY features features
COPY scripts/runtime scripts/runtime
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./services/api/cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./services/api/cmd/migrate \
    && POSTQRON_BUILD_SERVER_FACTORIES=0 ./scripts/runtime/bundle-features.sh features /out/features

FROM alpine:3.23 AS runtime

RUN addgroup -S postqron \
    && adduser -S -G postqron postqron \
    && mkdir -p /var/lib/postqron/privacy-exports \
    && chown -R postqron:postqron /var/lib/postqron \
    && chmod 0700 /var/lib/postqron/privacy-exports
WORKDIR /app
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY --from=build /out/features ./features
COPY features/f01-brand/tokens ./features/f01-brand/tokens
COPY services/api/features ./foundation

USER postqron
EXPOSE 8080
ENV API_ADDR=:8080
ENV POSTQRON_FEATURE_ROOTS=/app/foundation:/app/features
CMD ["api"]
