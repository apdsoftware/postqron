# F17 — Editorial collaboration

This autonomous API slice adds internal comments and an approval workflow to
composer drafts.

## Authorization and review rules

- F4 adapters implement `Authorizer`; the request never supplies a trusted
  role. Active Owner and Member memberships can receive comment and
  review-request permissions. Approval and comment resolution are separate
  server-side permissions intended for Owners.
- F6 adapters implement `DraftReader`. A review request is accepted only when
  the authoritative composer validation is valid and the expected revision
  still matches.
- A reviewer cannot approve a review they requested. Requesting changes
  requires a note, and approval is blocked while any comment is unresolved.
- `AuthorizeScheduling` is the F7 fail-closed boundary. It compares the
  scheduler request with both the current F6 revision and an approval for that
  exact revision. Editing a draft therefore invalidates scheduling without
  mutating review history.

F7 and F9 are consumed through explicit ports because their slices are not
present on the current base branch. The manifest declares the discovered F4
and F6 producers; adding nonexistent dependency IDs would make F16 discovery
reject the repository.

## Audit and F9 events

Each successful comment or review mutation atomically writes:

1. a minimal immutable audit row; and
2. a versioned transactional outbox event for F9.

Blocked scheduling attempts are recorded with outcome `denied`. Events contain
opaque IDs and review status only; comment bodies are deliberately excluded.
`PendingEvents` and idempotent `MarkEventPublished` form the dispatcher
boundary.

## HTTP API

The authenticated endpoints below
`/api/v1/workspaces/{workspace_id}/drafts/{draft_id}` are:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET`, `POST` | `/comments` | List or create comments |
| `POST` | `/comments/{comment_id}/resolve` | Resolve with reviewer permission |
| `GET`, `POST` | `/review` | Read or request review |
| `POST` | `/review/{review_id}/decision` | Approve or request changes |

The complete wire contract is in `contracts/openapi.yaml`; the F9 event schema
is in `contracts/events/v1`.

## Verification

Run the slice independently because issue #22 may only modify this directory:

```sh
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Validate F16 discovery and the forward-only migration from the repository
root:

```sh
POSTQRON_FEATURE_ROOTS="services/api/features:features" pnpm migrations:check
```

With the migration applied to a disposable PostgreSQL database:

```sh
F17_DATABASE_URL="postgres://..." GOWORK=off \
  go test -race -run TestPostgresRepositoryIntegration ./...
```
