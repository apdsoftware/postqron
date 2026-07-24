# F8 — Reliable publishing

This worker slice consumes the durable publication command produced by F7 and
executes one immutable F5/F6 destination snapshot at a time.

## Guarantees

- A command and each channel destination are inserted idempotently.
- PostgreSQL claims use `FOR UPDATE SKIP LOCKED`, a durable lease token, and an
  attempt ledger. An expired claim is reclaimable after a worker crash.
- Every provider request receives the persisted destination idempotency key.
  A provider adapter **must** durably map that key to one remote publication
  and return the same remote ID on replay. Adapters that cannot meet this
  contract must not be exposed by runtime discovery.
- Status, diagnostic detail, attempt count, and remote ID are stored per
  destination. The job status is an aggregate only.
- Retryable failures use bounded exponential backoff and provider `Retry-After`
  hints. Permanent failures and exhausted retries enter the DLQ.
- Manual retry requires authorization, resolves the open DLQ record, resets
  the automatic-attempt budget, and preserves the original idempotency key.
- The F7 command gate is checked both when accepting and immediately before
  executing work, so invalidated generations are cancelled.

Opaque connection references are persisted, never provider tokens. Diagnostic
text is redacted and length-limited before storage.

## Integration

F16 discovers `feature.yaml`; no central registry change is needed. The worker
injects implementations of:

- `CommandGate` from F7;
- `PublisherResolver` from discovered F5 provider adapters;
- `RetryAuthorizer` from the workspace authorization boundary.

The immutable destination payload is prepared from validated F6 content.

## Verification

```sh
cd features/f08-publishing
go test ./...
go vet ./...
```

Set `F08_DATABASE_URL` to run the optional PostgreSQL integration test after
the repository migration runner has applied this slice.
