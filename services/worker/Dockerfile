FROM golang:1.26-alpine AS build

WORKDIR /workspace
COPY go.work ./
COPY packages/runtime/go/go.mod packages/runtime/go/go.sum packages/runtime/go/
COPY services/api/go.mod services/api/go.sum services/api/
COPY services/worker/go.mod services/worker/go.sum services/worker/
RUN go mod download -C services/worker

COPY packages/runtime/go packages/runtime/go
COPY services/worker services/worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./services/worker/cmd/worker

FROM alpine:3.23 AS runtime

RUN addgroup -S postqron && adduser -S -G postqron postqron
WORKDIR /app
COPY --from=build /out/worker /usr/local/bin/worker
COPY services/worker/features ./features

USER postqron
ENV POSTQRON_FEATURE_ROOTS=/app/features
CMD ["worker"]
