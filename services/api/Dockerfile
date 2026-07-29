FROM golang:1.26-alpine AS build

WORKDIR /workspace
RUN apk add --no-cache bash
COPY go.work ./
COPY packages/runtime/go/go.mod packages/runtime/go/go.sum packages/runtime/go/
COPY services/api/go.mod services/api/go.sum services/api/
COPY features/f10-entitlements/go.mod features/f10-entitlements/go.sum features/f10-entitlements/
COPY services/worker/go.mod services/worker/go.sum services/worker/
RUN go mod download -C services/api

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
