# Operational incident runbook

Alerts carry stable codes and aggregate measurements only. Do not paste tokens,
personal data, post content, provider payloads, raw IP addresses, connection
strings, or tenant identifiers into alert annotations or shared incident chat.

## Publication queue

`PostqronPublicationQueueDelayed` is critical when queue depth exceeds 100 or
the oldest durable job waits more than 60 seconds for five minutes.

1. Confirm the durable database queue is authoritative and compare worker
   readiness, lease age, retry state, and provider-specific latency.
2. Stop nonessential deploys. Scale healthy workers only after verifying leases
   and idempotency keys.
3. Quarantine poison jobs without deleting durable state. Never manually replay
   a job unless its idempotency guard and cancellation state have been checked.
4. If the provider is degraded, preserve controlled backoff and bound retries;
   do not create an unbounded retry storm.
5. Close only after the oldest-job age is below the 60-second objective and
   delayed jobs have terminal, user-visible outcomes.

## Publication failures

`PostqronPublicationFailures` warns on any new terminal failure.

1. Group failures by bounded provider/error class metrics, never by tenant,
   account, post, payload, or remote identifier.
2. Distinguish permanent validation/revocation errors from temporary provider
   errors and internal defects.
3. Verify the user-visible status and transactional failure notification.
4. Requeue only explicit retryable classes and preserve the idempotency key.

## Readiness

`PostqronServiceNotReady` is critical after two minutes.

1. Inspect the dependency status name returned by `/readyz`; response bodies
   intentionally omit raw errors.
2. Use restricted, redacted structured logs to diagnose database, durable queue,
   secret-provider, or required downstream availability.
3. Keep the instance out of traffic until all required checks succeed.

## Audit write failures

`PostqronSensitiveAuditWriteFailed` pages immediately.

1. Treat the associated sensitive mutation as failed unless its transaction and
   audit append are proven atomic.
2. Restrict privileged mutations while the audit sink is unavailable.
3. Restore append capability; never update or delete an existing audit row.
4. Reconcile attempted operations from transactional identifiers without
   copying payloads into audit.
5. Notify Security Owner and preserve evidence under restricted access.

## Rate-limit spikes

Use the rejection counter only as an aggregate indicator. Confirm limits are
keyed from authenticated server context or a trusted edge address, not a
spoofable client header. Adjusting a threshold requires Security review and a
dated exception; never disable the limit to clear an alert.
