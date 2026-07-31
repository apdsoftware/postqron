# F7 — Calendar and scheduling

This slice owns scheduled-post state, calendar queries, local-time resolution,
and the durable command handed to F8. Runtime discovery uses `feature.yaml`; no
central registry is required.

## API runtime

The API runtime mounts the F7 routes through feature discovery. Private methods
consume the shared `__Host-postqron_session` authentication context and require
an active Owner or Member whose selected F4 workspace matches `workspace_id`.
Browser origins come from the exact `POSTQRON_AUTH_ALLOWED_ORIGINS` allowlist;
credentialed CORS is applied before authentication so `401` responses and
public `OPTIONS` preflight responses remain consumable by the web app.
Preflight permits `Idempotency-Key` for the create and duplicate operations.
Allowed browser responses expose `Location` and `Idempotency-Replayed`.

F7 now consumes the F6 `SchedulingBoundary` for draft validation and exact
revision-aware duplication. Draft state remains owned by F6; F7 persists the
validated `draft_revision` on each scheduled post and publication command, then
invalidates stale commands whenever a post is edited, rescheduled, or
cancelled. If the boundary is genuinely unavailable, create/edit/duplicate fail
closed with `503 scheduling_dependency_unavailable`; calendar, get, reschedule,
and cancel remain mounted independently. The browser-safe response
intentionally omits publication command ids and account ids.

## Time contract

Clients submit a wall time and an explicit IANA zone:

```json
{
  "local_date_time": "2026-10-25T02:30:00",
  "time_zone": "Europe/Rome",
  "utc_offset_minutes": 60
}
```

The service stores the canonical UTC instant together with the original local
time, IANA zone, and UTC offset. A skipped DST wall time is rejected. A repeated
DST wall time requires `utc_offset_minutes`, so the chosen occurrence is never
implicit. Scheduling in the past is rejected before composer validation or any
write.

## API

- `GET /api/v1/workspaces/{workspace_id}/calendar?from=<RFC3339>&until=<RFC3339>&channel_id=<id>&status=<status>`
- `POST /api/v1/workspaces/{workspace_id}/scheduled-posts`
- `GET|PUT /api/v1/workspaces/{workspace_id}/scheduled-posts/{post_id}`
- `POST .../{post_id}/reschedule`
- `POST .../{post_id}/duplicate`
- `POST .../{post_id}/cancel`

`POST /scheduled-posts` and `POST .../{post_id}/duplicate` require a visible
ASCII `Idempotency-Key` header of at most 200 bytes. The key is scoped by
workspace and operation and is bound to a canonical payload fingerprint. F7
stores only a SHA-256 digest of the client key, never the raw header. A
completed retry returns the immutable browser-safe `201` response snapshot,
the same `Location`, and `Idempotency-Replayed: true`, even after the post is
edited, rescheduled, cancelled, or published. Reusing
the key with different input returns non-retryable
`409 idempotency_payload_mismatch`; a concurrent fenced owner returns retryable
`409 idempotency_in_progress`. Operations completed before immutable response
snapshots existed remain bound to their original key and payload, but replay
fails closed with non-retryable `409 idempotency_replay_unavailable`; F7 never
fabricates the historical `201` from mutable current post state.

Calendar ranges are half-open (`from <= scheduled_for_utc < until`) and may be
filtered by connected channel and status. Mutations require
`expected_revision`; only `scheduled` posts can be changed.

`ContentGateway` is the explicit F6 boundary. It validates the exact requested
channel set against one immutable draft revision and creates an independent
draft snapshot when duplicating a scheduled post. `Authorizer` is the workspace
authorization boundary.

The versioned HTTP contract is in `contracts/scheduling.openapi.yaml`; matching
browser types are in `client/contracts.ts`.

## Atomicity and invalidation

Every scheduled post and its first `PublicationCommand` are inserted in one
transaction. Edit and reschedule lock the post, invalidate its pending command,
advance the revision, and insert the replacement command in the same
transaction. Cancel invalidates without replacement. Duplicate atomically
checks the source revision and inserts a new post/command pair.

The partial unique index permits only one pending command per post. F8 must
execute a command only if it is still `pending` and its ID still equals the
post's `active_command_id`; `post:generation` is the idempotency key.

HTTP create idempotency is persisted in `f07_idempotency_operations` before F6
validation. The first reservation fixes the post and publication-command IDs;
completion inserts both records and marks the reservation completed in the
same PostgreSQL transaction while capturing the canonical response snapshot.
Consequently a retry recovers the original committed response
even when the client lost the original response or the commit result was
ambiguous.

Duplicate uses a durable F7 saga: `reserved → prepared → clone_created →
completed`. `prepared` contains the exact source draft revision, channels and
schedule before F6 is called. Preparation locks and checks the source; final
completion locks it again and requires the same scheduled revision. If an edit
or cancellation wins between those points, F7 returns `revision_conflict`,
keeps the clone reference in `clone_created`, and inserts no scheduled post or
publication command. F6 receives a deterministic key derived from the
F7 reservation. F7 then persists the returned clone reference before inserting
the scheduled post and command. A failed request is recovered by retrying the
same HTTP key: it resumes from the recorded state and never calls F6 again once
`clone_created` is durable. A five-minute lease and monotonically increasing
generation fence stale owners; an exiting failed owner releases its lease for
immediate recovery. This makes a completed F6 clone reachable and finalizable
without requiring an F6 delete boundary or modifying F6-owned state.

The idempotency table contains no account identifier and its response snapshot
uses the browser-safe view, which omits account attribution. A workspace
foreign key with `ON DELETE CASCADE` removes completed and in-progress
reservations during the existing privacy workspace deletion flow. This is the
F7 privacy hook: account erasure requires no F7 call because actor attribution
is not retained, while workspace erasure is enforced by the database even when
the privacy runtime does not know about this table.

## Verification

```sh
GOWORK=off go test -race ./...
```

The PostgreSQL integration test is opt-in after applying the feature migration:

```sh
F07_DATABASE_URL=postgres://... GOWORK=off go test -race ./...
```
