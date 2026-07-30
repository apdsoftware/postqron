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

The default runtime registry is deliberately empty and fail-closed. Issue
[#329](https://github.com/apdsoftware/postqron/issues/329) owns the F5→F8
credential boundary and official publishing adapters for the D2 matrix. Until
that dependency lands, F8 provides the durable engine and adapter contract but
does not claim that any social provider is runtime-publishable.

## Verification

```sh
cd features/f08-publishing
go test -race ./...
go vet ./...
```

Set `F08_DATABASE_URL` to run the optional PostgreSQL integration test after
the repository migration runner has applied this slice.
