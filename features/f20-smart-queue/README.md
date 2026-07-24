# F20 — Smart queue

This autonomous API slice owns recurring publication windows and puts a draft
in the first available slot only after an explicit preview and confirmation.

## Behavior

- every queue stores an explicit IANA time zone, a weekly set of non-overlapping
  local-time windows, a slot interval and a bounded search horizon;
- slot generation skips nonexistent wall times during a spring DST transition;
- when a wall time occurs twice during a fall transition, both instants are
  considered and ordered by UTC, including non-hour offset changes;
- preview starts at the later of `now` and `not_before_utc`, stops at the
  earliest of the queue horizon, the trusted F10 horizon and `until_utc`, and
  excludes both F20 reservations and instants reported by F7;
- previews are opaque, expire after ten minutes and carry the queue revision;
  clients never submit the selected instant during confirmation;
- confirmation locks the preview, verifies its expiry and queue revision,
  enforces the trusted F10 pending-reservation capacity, reserves the slot with
  a unique constraint and writes the F7 scheduling command in one transaction;
- `(workspace_id, idempotency_key)` makes exact confirmation retries safe.
  Reuse with a different preview or payload returns `idempotency_mismatch`;
- conflict codes are stable: `slot_unavailable`, `preview_expired`,
  `preview_consumed`, `queue_changed`, `capacity_exceeded`,
  `idempotency_mismatch`, and `revision_conflict`.

The F7 scheduling command is an internal transactional outbox record and is not
returned by the HTTP API. A trusted adapter consumes
`contracts/f07-scheduling-command.schema.json` and marks it sent only after F7
accepts the idempotent schedule request.

F10 capacities are read only through the server-side `Entitlements` boundary.
Browser requests cannot choose or increase plan limits. The expected contract
is documented in `contracts/f10-smart-queue-limits.schema.json`.

## HTTP API

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/workspaces/{workspace_id}/smart-queues` | Create recurring windows |
| `PUT` | `/api/v1/workspaces/{workspace_id}/smart-queues/{queue_id}` | Replace windows with optimistic revision |
| `POST` | `/api/v1/workspaces/{workspace_id}/smart-queues/{queue_id}/preview` | Preview the first available slot |
| `POST` | `/api/v1/workspaces/{workspace_id}/smart-queues/{queue_id}/confirm` | Atomically reserve the previewed slot |

The complete wire contract is in `contracts/openapi.yaml`.

## Runtime discovery

`feature.yaml` declares the discovered `scheduling` (F7) and
`f10-entitlements` dependencies. No central registry is required.

## Verification

```sh
cd features/f20-smart-queue
GOWORK=off go test -race ./...
GOWORK=off go vet ./...

cd ../..
POSTQRON_FEATURE_ROOTS="services/api/features:features" pnpm migrations:check
```

Set `F20_DATABASE_URL` after applying the migration to enable the PostgreSQL
contention integration test.
