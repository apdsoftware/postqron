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
  Instagram Personal remain production-disabled until #343 supplies confirmed,
  downstream-idempotent delivery.

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

Facebook Pages, Instagram Professional, and Threads are registered only when
their exact per-provider enable, App Review, runtime-audit, Graph-version, and
F5 credential dependencies are complete. Missing or partial dependencies abort
bootstrap. Facebook Groups and Instagram Personal have a durable F8 outbox and
claim/retry dispatcher implementation, but their production registration is
rejected with an explicit #343 dependency; they must not be described as
production-ready.

Media containers, carousel children, final publish IDs, and permalink reads are
separate durable steps. Meta does not claim general reconciliation: an
ambiguous POST without a durable provider-visible ID or a crash-reclaimed lease
is dead-lettered without a second POST. Provider capabilities contain the
canonical supported formats, media kind, cardinality, and text limit used by
runtime validation.

Production gates:

- `POSTQRON_F08_META_AUTO_ENABLED=true` enables consideration of auto adapters.
- `POSTQRON_F08_FACEBOOK_PAGES_ENABLED=true`,
  `POSTQRON_F08_INSTAGRAM_PROFESSIONAL_ENABLED=true`, and
  `POSTQRON_F08_THREADS_ENABLED=true` enable each reviewed provider.
- Existing F5 cipher, Meta, App Review, and runtime-audit gates remain
  authoritative for Facebook Pages and Instagram Professional.
- Threads additionally requires its explicit F8 Graph version, OAuth client,
  redirect, App Review, and runtime-audit gates.
- `POSTQRON_F08_META_NOTIFICATIONS_ENABLED=true` intentionally fails bootstrap
  until #343 is integrated.

## Verification

```sh
cd features/f08-publishing
go test -race ./...
go vet ./...
```

Set `F08_DATABASE_URL` to run the optional PostgreSQL integration test after
the repository migration runner has applied this slice.
