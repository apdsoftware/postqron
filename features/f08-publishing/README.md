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
  explicit ambiguous-fail-closed behavior. A lease-expired `publishing` claim
  is marked ambiguous: reconciliation runs first when declared; a
  native-idempotent adapter replays the same key; a fail-closed adapter enters
  the DLQ without replay.
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
- Notification publishing is a separate idempotent delivery boundary and ends
  in `notified` with a durable delivery ID; it never fabricates a social remote
  ID or calls a social provider.

Opaque connection references are persisted, never provider tokens. Diagnostic
text is redacted and length-limited before storage.

## Integration

F16 discovers `feature.yaml`; no central registry change is needed. The worker
injects implementations of:

- `CommandGate` from F7;
- `PublisherResolver` and `NotificationResolver` from discovered F8 adapters;
- `RetryAuthorizer` from the workspace authorization boundary.

The immutable destination payload is prepared from a validated F6 revision.

The runtime contains official TikTok Direct Post and YouTube Shorts adapters.
They call providers only through F5 `AuthenticatedExecutor`, with an explicit
`ExpectedProvider` on every request. Registration remains fail-closed unless
configuration, provider review, runtime audit, and quota verification gates
are all true. TikTok additionally requires a verified immutable pull prefix
and explicit F5 trailing-slash support. The latter is tracked by #342; PR #339
must not be approved until #342 is integrated into its base and this branch is
rebased. The default registry therefore remains empty.

TikTok checkpoints creator capability discovery, Direct Post initialization,
and asynchronous status. Its three official API paths preserve their required
trailing slash. It uses a verified, immutable HTTPS pull URL so the
upload capability URL never crosses the F5 boundary. YouTube checkpoints
channel capability, a local `multipart/related` upload through F5, and
asynchronous processing. TikTok reconciles only from a durable `publish_id`.
YouTube does not claim deterministic reconciliation for a pre-ID multipart
outcome; it declares the F8 ambiguous-fail-closed capability, which prevents
all blind upload replay.

## Verification

```sh
cd features/f08-publishing
go test -race ./...
go vet ./...
```

Set `F08_DATABASE_URL` to run the optional PostgreSQL integration test after
the repository migration runner has applied this slice.
