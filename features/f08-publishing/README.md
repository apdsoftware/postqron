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
  Registration requires native idempotency or deterministic reconciliation.
  A lease-expired `publishing` claim is marked ambiguous: reconciliation runs
  first when declared; a native-idempotent adapter replays the same key.
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

The dynamic provider package implements only Mastodon and Bluesky:

- Mastodon reads the connected instance capability document, uploads each
  attachment through a durable multipart checkpoint, waits for asynchronous
  processing, and creates the status with the official `Idempotency-Key`.
- Bluesky uploads image blobs through the connected PDS, creates
  `app.bsky.feed.post` with a deterministic, Lexicon-valid TID, and reconciles
  that exact key through `com.atproto.repo.getRecord`.

Both adapters call only the F5 `AuthenticatedExecutor`, always set
`ExpectedProvider`, and never receive origins, tokens, PAR state, DPoP keys, or
nonces. Discovery, DNS pinning, dynamic OAuth/DPoP refresh and nonce persistence
remain inside F5.

The worker registers either adapter only when a trusted executor is injected
and all provider gates (configured, reviewed, runtime-audited, and
quota-verified) are true. Missing dependencies or evidence leave the provider
unregistered and fail-closed. Ambiguous media uploads are never retried blindly;
ambiguous final creates use native Mastodon idempotency or deterministic
Bluesky reconciliation before another write.

## Verification

```sh
cd features/f08-publishing
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Set `F08_DATABASE_URL` to run the optional PostgreSQL integration test after
the repository migration runner has applied this slice.
