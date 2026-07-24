# F18 — Essential analytics

This autonomous slice consumes successful F8 publication destinations and
stores provider insight snapshots for the launch channels selected by D2:
Facebook Pages and Instagram Professional accounts.

## Boundaries

- `RegisterPublished` accepts only Meta launch channels and keeps the
  workspace, content, channel, connection and remote publication IDs together.
  It never copies provider credentials.
- `PermissionReader` checks the effective F5 grant before every sync:
  `read_insights` for Facebook Pages and
  `instagram_business_manage_insights` for Instagram Professional.
- `ProviderResolver` supplies adapters through F16 discovery. The worker passes
  only D2-normalized metric names; adapters retain the original provider name,
  period and explicit API version in every result.
- `RateLimiter.Reserve` always receives `analytics_low`, so an implementation
  can preserve the last provider capacity for F8 publication.

## Incremental synchronization

`SyncOne` claims one due target with a persistent lease, sends its opaque cursor
to the adapter, and advances the cursor atomically with the returned
observations. Temporary errors use exponential backoff; provider
`Retry-After` is a lower bound and is never capped. Capacity deferrals do not
consume failure attempts. Terminal failures are recorded as metric states
instead of being rendered as zero.

Provider adapters return exactly one state for every requested metric:
`available` with a non-negative integer (including `0`), or `unavailable`
without a value. The slice adds `permission_missing` and `failed` states.

## Overview API

```text
GET /api/v1/workspaces/{workspace_id}/analytics
    ?from=2026-07-01T00:00:00Z
    &to=2026-08-01T00:00:00Z
    &channel_id=optional-repeatable-filter
```

The authenticated endpoint includes content published in the half-open
`[from,to)` interval. It sums each target's latest available lifetime snapshot
per channel and exposes state counts. A JSON `value: 0` therefore remains
different from `value: null` with `unavailable`, `permission_missing`, or
`failed`; mixed target states are returned as `mixed`.

## Verification

```sh
cd features/f18-analytics
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```
