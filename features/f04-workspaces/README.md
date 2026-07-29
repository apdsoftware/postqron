# F04 — Workspaces, membership, and RBAC

This autonomous API slice owns the workspace domain described by D06:

- one idempotent personal workspace and Owner membership per account;
- app-runtime onboarding, authenticated workspace selection, and current
  workspace/member reads for F30;
- seven-day, single-use invitations whose tokens and target emails are stored
  only as digests;
- active membership uniqueness per account/workspace and fixed Owner/Member
  authorization;
- workspace and active-member reads for both roles, with Owner-only settings
  and pending-invitation reads;
- Owner-only member, channel, workspace, billing, ownership, and deletion
  operations;
- transactional member-capacity reservations and final-Owner protection;
- immutable audit events without invitation tokens or email addresses.

`Service.Authorize` reads the membership role from the repository. Callers
never submit a trusted role. Every sensitive PostgreSQL mutation repeats the
Owner check inside a serializable transaction after locking the workspace row.
The deferred database trigger is an additional final-Owner backstop.

The `MemberLimitProvider` is the boundary to F10. `-1` means unlimited; a
missing, zero, or invalid entitlement fails closed.

## Runtime onboarding

`NewPostgresModule` now exports the F4 runtime handlers consumed by F30:

- `POST /api/v1/app/onboarding`
- `POST /api/v1/app/workspaces/select`
- `GET /api/v1/app/workspaces/current`
- `GET /api/v1/app/workspaces/current/members`

The runtime path authenticates the F3 `__Host-postqron_session` cookie
server-side, never trusts client-supplied account IDs, validates the two
required onboarding consent receipts against the current approved compliance
documents, records consent evidence idempotently, and persists the selected
workspace in `f04_workspace_selections`.

This slice is intentionally self-contained for issue `#217`. Mounting the new
module into the real API binary and generated runtime factories remains the
responsibility of issue `#220`.

## Verification

Run the slice tests independently because issue #10 may only change this
directory and therefore cannot add the module to the root `go.work`:

```sh
GOCACHE=$(pwd)/.cache/go-build GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Validate discovery and migration metadata from the repository root:

```sh
POSTQRON_FEATURE_ROOTS="services/api/features:features" pnpm migrations:check
```

On a disposable PostgreSQL database with the migrations already applied:

```sh
F04_DATABASE_URL="postgres://..." GOCACHE=$(pwd)/.cache/go-build GOWORK=off \
  go test -race -run 'TestPostgresRepositoryIntegration|TestPostgresRuntimeOnboardingIntegration' ./...
```
