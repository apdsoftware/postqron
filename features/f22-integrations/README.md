# F22 — Public API and webhooks

This autonomous slice exposes a secure, versioned integration boundary for
CMSs, automation tools, and analytics consumers. It owns its manifest,
contracts, migration, implementation, tests, and operational guidance; F16
discovers it without a central registry.

## Public API

- Base path: `/api/public/v1`.
- Bearer credentials are pinned to one workspace, carry an explicit expiry and
  only the scopes `posts:read`, `posts:write`, `webhooks:read`, or
  `webhooks:write`. F3 authenticates a digest of the credential and F4 confirms
  current membership/RBAC on every request.
- Rate-limit keys come from the authenticated credential ID. Never trust an IP,
  workspace, or credential ID supplied in a request header.
- Collection responses use an opaque, HMAC-authenticated, workspace-bound
  cursor that expires after 24 hours. Page size is capped at 100.
- Mutating operations require an `Idempotency-Key`. The key, operation,
  credential, workspace, and request fingerprint are serialized and retained
  for 24 hours. Identical retries replay the response; a changed request
  returns `409 idempotency_conflict`.
- Public post resources never contain OAuth credentials, social-provider
  tokens, or raw provider responses.

The OpenAPI contract is in `contracts/openapi.yaml`.

## Webhooks

`WebhookPublisher` validates a dated event envelope, rejects credential-shaped
payload fields recursively, and atomically enqueues one delivery per active
subscription. Each attempt signs the exact raw body using HMAC-SHA256:

```text
Postqron-Signature: t=<unix-seconds>,v1=<hex-hmac>
signed-value = <unix-seconds> + "." + <raw-request-body>
```

Consumers should use `VerifyWebhook` (or the equivalent constant-time
implementation), require version `2026-07-01`, and reject timestamps outside a
five-minute window. Event IDs are stable across retries and must be processed
idempotently.

The worker applies an explicit 10-second default timeout, never follows
redirects, rejects non-public destinations both when configured and after DNS
resolution, and retries network errors, `408`, `425`, `429`, and `5xx`
responses. Exponential backoff has stable jitter and a six-hour cap. Other
`4xx` responses and exhausted retries move the delivery to the dead-letter
queue.

Signing secrets must contain at least 32 random bytes, are returned only when a
subscription is created, and are envelope-encrypted by the secret-manager
adapter before persistence. They are not API/provider tokens and never appear
in read responses, logs, metrics, errors, or DLQ records.

See `docs/webhooks.md` for the consumer and operational runbook and
`contracts/webhook-event.schema.json` for the event contract.

## Observability and retention

`WebhookMetrics` exports a bounded, label-free metric set for enqueue,
delivery, retry, DLQ, and last successful duration. Structured observations
contain only bounded metadata; payloads, endpoints, workspace IDs, signing
secrets, and response bodies are excluded. Alert on a rising DLQ total,
sustained retries, and stale pending rows.

The migration provides expiry indices. Run retention jobs at least hourly:

- idempotency responses: delete after 24 hours;
- completed webhook events/deliveries: delete after 30 days;
- dead letters: inspect or replay through an audited operator flow, then delete
  after 30 days.

## Host adapters

Production hosts provide:

- `Authenticator` from F3 and `Authorizer` from F4;
- F15's shared `RateLimiter`, redacted observer, metrics endpoint, and secret
  manager;
- a PostgreSQL implementation of `IdempotencyStore`, `WebhookQueue`, and
  `WebhookSubscriptionRepository`;
- the content slice's `PostGateway`.

No adapter may place a social-provider token in F22 types or storage.

## Verification

Run the slice tests:

```sh
cd features/f22-integrations
GOWORK=off go test -race ./...
```

Validate discovery and the migration together with its declared dependencies:

```sh
go run ./services/api/cmd/migrate --check \
  --roots services/api/features:features
```
