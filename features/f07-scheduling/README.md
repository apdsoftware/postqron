# F7 — Calendar and scheduling

This slice owns scheduled-post state, calendar queries, local-time resolution,
and the durable command handed to F8. Runtime discovery uses `feature.yaml`; no
central registry is required.

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

Calendar ranges are half-open (`from <= scheduled_for_utc < until`) and may be
filtered by connected channel and status. Mutations require
`expected_revision`; only `scheduled` posts can be changed.

`ContentGateway` is the explicit F6 boundary. It validates a draft against the
selected channels and creates an independent draft when duplicating a post.
`Authorizer` is the workspace authorization boundary.

## Atomicity and invalidation

Every scheduled post and its first `PublicationCommand` are inserted in one
transaction. Edit and reschedule lock the post, invalidate its pending command,
advance the revision, and insert the replacement command in the same
transaction. Cancel invalidates without replacement. Duplicate atomically
checks the source revision and inserts a new post/command pair.

The partial unique index permits only one pending command per post. F8 must
execute a command only if it is still `pending` and its ID still equals the
post's `active_command_id`; `post:generation` is the idempotency key.

## Verification

```sh
GOWORK=off go test -race ./...
```

The PostgreSQL integration test is opt-in after applying the feature migration:

```sh
F07_DATABASE_URL=postgres://... GOWORK=off go test -race ./...
```
