# F31 — Protected admin console

This slice owns `/admin` and its dedicated sections, its five-locale catalog,
the private admin HTTP adapter, and the security policy applied to every
admin operation.

## Responsive shell and navigation

The console renders through a single layout (`layouts/admin-console.vue`)
shared by every route:

- `/admin` — Dashboard (the `/admin` landing page): service health and
  top-level KPIs.
- `/admin/users` — search registered users.
- `/admin/workspaces` — search workspaces.
- `/admin/plans` — review entitlements and assign or revoke the internal plan.
- `/admin/audit` — immutable audit activity.
- `/admin/profile` — the authenticated administrator's own identity.

The layout shows a persistent sidebar with the six sections above on desktop
and a focus-managed, `Escape`-dismissible drawer below `~980px`. The top bar
carries the current section title, the administrator identity, a link to the
profile route, the compact `<select>`-based `PostqronLanguageSwitcher`, and a
reserved sign-out control. When no admin session is present, the layout
renders an inline email/password login gate instead of the sidebar and
delegates to the same `admin-access` middleware and session state used by
every route, so no page can bypass the gate. Reusable building blocks
(`components/AdminPageHeader.vue`, `AdminAlert.vue`, `AdminKpiCards.vue`,
`AdminTable.vue`, `AdminPagination.vue`, `AdminSearchFilter.vue`) keep every
page's heading, empty/error state, table, and pagination consistent.

## Security boundary

The HTTP adapter must be mounted only on the authenticated private channel
provided by F24. It intentionally is not declared as a public server route in
`feature.yaml`. `Handler` authenticates every request and returns `403` before
reading admin data when the verified, normalized session email is not both
active in the admin directory and present in the server allowlist.

The initial production configuration is:

```sh
POSTQRON_ADMIN_ALLOWLIST=carlo.zuffetti@apdsoftware.it
POSTQRON_ADMIN_ALLOWED_ORIGINS=https://postqron.com
```

The address is configuration, not an authentication bypass. The admin surface
uses the central F3 email/password session; optional OAuth providers are not
required. Access also requires a valid session, verified email, an active
server-side admin record, and an unexpired session. `Service.BootstrapAdmins`
applies configuration changes through the same immutable audit boundary used
for later administrator additions and removals.

Mutations additionally require:

- the session CSRF token in `X-CSRF-Token`;
- authentication no older than the configured re-authentication window;
- a unique `Idempotency-Key`;
- explicit confirmation and a non-empty reason;
- server-side authorization again at execution time.

Internal-plan changes are delegated to the private F11 client. F31 never
exposes the internal plan in public catalogs and never reads social tokens,
secrets, or complete payment data.

## Private adapter

`NewPostgresModule` creates the service with an allowlist parsed from
`POSTQRON_ADMIN_ALLOWLIST` and a non-empty, normalized browser-origin allowlist
parsed from `POSTQRON_ADMIN_ALLOWED_ORIGINS`, calls `BootstrapAdmins` during
startup, and exposes `NewHandler(service, authenticator, allowedOrigins...)`
through F24's route overlay. The hybrid manifest declares every `GET`, `PUT`,
and `DELETE` adapter route except `GET /admin/session` as `private`.
The session transport and each matching `OPTIONS` pattern are declared
separately on the public transport channel so anonymous 401 responses and
preflights can reach the F31 origin policy before the private channel's
session authentication. The F31 handler still authenticates and allowlists
the session request itself. Public preflight routes return no admin data, and
the same handler rejects every origin outside the configured allowlist.

Allowed browser origins receive an exact `Access-Control-Allow-Origin`,
`Access-Control-Allow-Credentials: true`, and `Vary: Origin`. Their preflight
requests complete before authentication and advertise only `GET`, `PUT`,
`DELETE`, `Content-Type`, `X-CSRF-Token`, and `Idempotency-Key`. Requests
without `Origin` keep the server-to-server behavior; configured browser
origins are never inferred or reflected.
The adapter provides:

- `GET /api/v1/admin/session`
- `GET /api/v1/admin/dashboard`
- `GET /api/v1/admin/search?q=…`
- `PUT|DELETE /api/v1/admin/workspaces/{id}/internal-plan`
- `PUT|DELETE /api/v1/admin/admins/{id}`

The Nuxt runtime expects that private listener behind its configured API
boundary. No provider credential is sent to the browser.

## Dashboard service health

`GET /api/v1/admin/dashboard` never reports a service `operational` from a
hardcoded value or from missing data. `PostgresStore.Dashboard` collects one
real, timestamped signal per essential dependency — `api` and `database` from
a bounded ping, `worker_queue` from the real F08 publishing backlog, and
`scheduler_publishing` from the real F07 pending-command backlog — and
projects each through F15's `operations.ProjectServiceStatus` adapter
(`operational`, `degraded`, `outage`, or `unknown`). A dependency that was
never checked, or whose check has expired past the server's freshness
window, is `unknown`; it is never rendered as operational. A database outage
degrades the response (api/database report the outage, worker/scheduler
report unknown) instead of failing the whole request, matching the
`AdminDashboard` schema's partial-failure behavior documented in
`contracts/admin.openapi.yaml`.

On the client, `pages/admin.vue` shows a high-contrast `AdminAlert` banner
naming every non-operational service and its last-checked timestamp whenever
one exists — status is never conveyed by color alone. `useAdminSectionLoad`
supports a controlled background refresh (`intervalMs`) with per-request
`AbortController` cancellation and exponential backoff on repeated failure;
it never polls faster than the configured interval.

## Tests

```sh
pnpm --dir features/f31-admin-console test
GOWORK=off go test -race ./...
pnpm --dir features/f31-admin-console typecheck
```
