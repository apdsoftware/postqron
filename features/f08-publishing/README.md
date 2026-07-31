# F8 — Reliable capability-driven publishing

This worker slice consumes the durable publication command produced by F7 and
executes one immutable F5/F6 destination snapshot at a time.

## Guarantees

- A command and each destination/revision are inserted idempotently. Canonical
  payload, draft revision, provider mode, capability version, and adapter
  safety declaration form an immutable, database-enforced snapshot.
- PostgreSQL claims use `FOR UPDATE SKIP LOCKED`, a durable lease token, and an
  attempt ledger. An expired claim is reclaimable after a worker crash.
- Every provider request receives the persisted destination idempotency key.
  Registration requires native idempotency, deterministic reconciliation, or
  an explicit fail-closed ambiguity policy.
  A lease-expired `publishing` claim is marked ambiguous: reconciliation runs
  first when declared; a native-idempotent adapter replays the same key. An
  adapter that cannot reconcile is dead-lettered without another provider call.
- Multi-step media/video adapters perform at most one remote side effect per
  call. F8 persists the checkpoint before the next step. Successful progress
  compensates the claim's failure-attempt budget.
- Status, redacted diagnostic detail, remote ID, HTTPS permalink, checkpoint,
  and ambiguity state are stored per destination.
- Retryable failures use bounded exponential backoff and provider `Retry-After`
  hints. Permanent failures and exhausted retries enter the DLQ.
- Manual retry requires authorization, resolves the open DLQ record, resets
  the automatic-attempt budget, and preserves the idempotency key and ambiguity
  flag. An ambiguous manual retry reconciles before any possible republish.
- The F7 command gate is checked both when accepting and immediately before
  executing work, so invalidated generations are cancelled.
- Notification publishing is a separate idempotent delivery boundary; it never
  fabricates a social remote ID or calls a social provider. Facebook Groups and
  Instagram Personal enqueue a minimized command whose owner recipient, locale,
  and target-specific F14 template are resolved server-side. F8 reaches
  `notified` only after F14 exposes a confirmed `delivered` state. A provider
  `queued`/F14 `accepted` receipt is not delivery confirmation and terminates
  F8 as a fail-closed permanent failure.

Opaque connection references are persisted, never provider tokens. Diagnostic
text is redacted and length-limited before storage.

## Integration

F16 discovers `feature.yaml`; no central registry change is needed. The worker
injects implementations of:

- `CommandGate` from F7;
- `PublisherResolver` and `NotificationResolver` from discovered F8 adapters;
- `RetryAuthorizer` from the workspace authorization boundary.

The immutable destination payload is prepared from a validated F6 revision.

The default runtime registry is deliberately empty and fail-closed. With the
explicit F8 auto-publishing gate enabled, the production worker constructs the
F5 `AuthenticatedExecutor` from the PostgreSQL credential store, external F5
cipher configuration, reviewed F5 adapters, DNS-pinned public Meta origins,
and the Meta response classifiers. F8 adapters still receive only that
executor and opaque connection IDs; they never receive tokens. Every request
supplies the exact `ExpectedProvider` confused-deputy guard.

Facebook Pages and Instagram Professional are registered only when their exact
per-provider enable, App Review, runtime-audit, Graph-version, and F5 credential
dependencies are complete. Threads remains explicitly fail-closed in production
until the verified F5 adapter from #309 / PR #316 is integrated; setting its F8
enable gate aborts bootstrap and never registers a substitute credential
adapter. Facebook Groups and Instagram Personal are registered only when their
notification gate and the F9/F14 email boundary are both present. Missing or
partial wiring aborts startup.

The notification command stores no social body, media URL, email address, or
credential. It keeps only identifiers, a one-way conflict fingerprint,
server-resolved locale/template metadata, bounded diagnostic codes, and a link
to the idempotent F14 delivery. Claims use `SKIP LOCKED` and a lease. Retry
exhaustion and terminal F14 states become permanent failures. If the email
provider call completed but the worker crashed before its local commit, the F14
row remains `sending`; after the ambiguity window it is deterministically
failed and never replayed. Terminal minimized audit metadata follows D05 and is
purged after 12 months.

Media containers, carousel children, final publish IDs, and permalink reads are
separate durable steps. Meta does not claim general reconciliation: an
ambiguous POST without a durable provider-visible ID or a crash-reclaimed lease
is dead-lettered without a second POST. Provider capabilities contain the
canonical supported formats, media kind, cardinality, and text limit used by
runtime validation.

Production gates:

- `POSTQRON_F08_META_AUTO_ENABLED=true` enables consideration of auto adapters.
- `POSTQRON_F08_FACEBOOK_PAGES_ENABLED=true`,
  `POSTQRON_F08_INSTAGRAM_PROFESSIONAL_ENABLED=true` enable each reviewed
  production provider.
- Existing F5 cipher, Meta, App Review, and runtime-audit gates remain
  authoritative for Facebook Pages and Instagram Professional.
- `POSTQRON_F08_THREADS_ENABLED=true` intentionally fails bootstrap until the
  F5 dependency in #309 / PR #316 is integrated.
- `POSTQRON_F08_META_NOTIFICATIONS_ENABLED=true` enables Facebook Groups and
  Instagram Personal only when the worker F9/F14 boundary is configured;
  otherwise bootstrap fails closed.

## Verification

```sh
cd features/f08-publishing
go test -race ./...
go vet ./...
```

Set `F08_DATABASE_URL` to run the optional PostgreSQL integration test after
the repository migration runner has applied this slice.
