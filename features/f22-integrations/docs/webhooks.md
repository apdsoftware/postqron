# Webhook consumer and operations guide

## Delivery contract

Postqron sends `POST` requests with `Content-Type: application/json` and these
headers:

| Header | Meaning |
| --- | --- |
| `Postqron-Delivery-ID` | Unique attempt series; stable across retries |
| `Postqron-Event` | Lowercase dotted event type |
| `Postqron-Event-Version` | Dated envelope version (`2026-07-01`) |
| `Postqron-Timestamp` | Unix seconds used by the signature |
| `Postqron-Signature` | `t=<timestamp>,v1=<HMAC-SHA256>` |

The receiver must preserve the raw request bytes for signature verification.
Compute HMAC-SHA256 over `<timestamp>.<raw-body>`, compare in constant time, and
reject timestamps more than five minutes from the receiver's clock. Parse JSON
only after verification. Store the event `id` before side effects and return a
`2xx` for an already processed event.

Example envelope:

```json
{
  "id": "evt_01JZ8Y0M4QW2K6T9N7F5H3A1BC",
  "type": "post.published",
  "version": "2026-07-01",
  "workspace_id": "9f355e66-678d-4a27-a9b8-daeb352ceb89",
  "occurred_at": "2026-07-24T14:30:00Z",
  "data": {
    "post_id": "post_01JZ8Y0FQG2EVNQXQ5QKJ21E6E",
    "status": "published"
  }
}
```

OAuth tokens, provider access/refresh tokens, authorization headers, cookies,
passwords, signing secrets, and provider response bodies are never valid event
data.

## Response and retry behavior

- Any `2xx`: delivery complete.
- Network error, timeout, `408`, `425`, `429`, or `5xx`: retry with exponential
  backoff and stable jitter. A valid `Retry-After` can extend the delay up to
  the six-hour cap.
- Other `4xx`: terminal consumer error, move directly to DLQ.
- Eight failed attempts: move to DLQ.
- Redirects are terminal and are never followed, preventing signature leakage
  to another host.

Endpoints must use public HTTPS. Loopback, private, link-local, multicast,
`.local`, and `localhost` destinations are rejected, including after DNS
resolution.

## Rotation

Create a replacement subscription to receive a new secret, deploy verification
for both old and new subscriptions, confirm successful delivery, then disable
the old subscription. Read operations never return a secret. Secret rotation
and DLQ replay must be authorized as workspace-owner actions and recorded by
F15's sensitive audit facility.

## DLQ runbook

1. Alert on increases in `postqron_webhook_dead_lettered_total`.
2. Inspect only delivery ID, event type, attempt count, bounded error code, and
   HTTP status. Do not copy payloads, endpoints, or secrets into logs/tickets.
3. Confirm the subscription is still authorized and the endpoint is healthy.
4. Requeue through an audited operator command using the existing event ID.
5. Verify a `2xx`, then mark the DLQ item resolved. Never create a new logical
   event for a retry.
6. Purge unresolved and resolved DLQ records after the documented 30-day
   retention window.

If failures indicate credential exposure, disable the subscription, rotate the
signing secret, and follow F15's incident-response runbook.
