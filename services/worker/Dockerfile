FROM golang:1.26-alpine AS build

WORKDIR /workspace
RUN apk add --no-cache bash
COPY go.work ./
COPY packages/runtime/go/go.mod packages/runtime/go/go.sum packages/runtime/go/
COPY services/api/go.mod services/api/go.sum services/api/
COPY features/f10-entitlements/go.mod features/f10-entitlements/go.sum features/f10-entitlements/
COPY services/worker/go.mod services/worker/go.sum services/worker/
RUN go mod download -C services/worker

COPY packages/runtime/go packages/runtime/go
COPY services/worker services/worker
COPY features features
COPY scripts/runtime scripts/runtime
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./services/worker/cmd/worker \
    && ./scripts/runtime/bundle-features.sh features /out/features

FROM alpine:3.23 AS runtime

RUN addgroup -S postqron && adduser -S -G postqron postqron
WORKDIR /app
COPY --from=build /out/worker /usr/local/bin/worker
COPY --from=build /out/features ./features
COPY services/worker/features ./foundation/worker
COPY services/api/features ./foundation/api

USER postqron
ENV POSTQRON_FEATURE_ROOTS=/app/foundation/worker:/app/foundation/api:/app/features
CMD ["worker"]
