# F31 — Protected admin console

This slice owns `/admin` and its dedicated sections, its five-locale catalog,
the private admin HTTP adapter, and the security policy applied to every
admin operation.

## Responsive shell and navigation

The console renders through a single layout (`layouts/admin-console.vue`)
shared by every route:

- `/admin` — Dashboard (the `/admin` landing page): service health and
  top-level KPIs.
- `/admin/users` — complete, server-paginated registered-user directory.
- `/admin/workspaces` — complete, server-paginated workspace directory.
- `/admin/plans` — review entitlements and assign or revoke the internal plan.
- `/admin/audit` — immutable audit activity.
- `/admin/profile` — the authenticated administrator's own identity.

The layout shows a persistent sidebar with the six sections above on desktop
and a focus-managed, `Escape`-dismissible drawer below `~980px`. The top bar
carries the current section title, the administrator identity, a link to the
profile route, the compact `<select>`-based `PostqronLanguageSwitcher`, and a
sign-out button that remains visible on desktop and mobile. Sign-out calls
F3's server-side `POST /api/v1/auth/logout` with the session CSRF token, waits
for revocation and cookie expiry, clears client session state, and returns to
the admin login gate with an accessible confirmation. When no admin session is
present, the layout
renders an inline email/password login gate instead of the sidebar and
delegates to the same `admin-access` middleware and session state used by
every route, so no page can bypass the gate. Reusable building blocks
(`components/AdminPageHeader.vue`, `AdminAlert.vue`, `AdminKpiCards.vue`,
`AdminTable.vue`, `AdminPagination.vue`, `AdminSearchFilter.vue`) keep every
page's heading, empty/error state, table, and pagination consistent.

`/admin/profile` displays only the administrator account identifier, verified
email supplied by the protected session, and authentication time. Its
accessible password form sends the current password, new password,
confirmation, and CSRF token directly to F3. On success it discards every
password field and reloads the protected session so the browser uses the
rotated cookie and CSRF token; all other account sessions have already been
revoked atomically by F3. Stable errors cover invalid current password,
confirmation mismatch, policy failure, invalid CSRF, stale/expired sessions,
concurrent changes, rate limiting, and temporary unavailability without
echoing submitted values.

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
- `GET /api/v1/admin/plans`
- `GET /api/v1/admin/plans/export?format=csv|xlsx`
- `GET /api/v1/admin/audit`
- `GET /api/v1/admin/audit/{event_id}`
- `GET /api/v1/admin/audit/export?format=csv|xlsx`
- `GET /api/v1/admin/users`
- `GET /api/v1/admin/users/{account_id}`
- `GET /api/v1/admin/users/export?format=csv|xlsx`
- `GET /api/v1/admin/workspaces`
- `GET /api/v1/admin/workspaces/export?format=csv|xlsx`
- `PUT|DELETE /api/v1/admin/workspaces/{id}/internal-plan`
- `PUT|DELETE /api/v1/admin/admins/{id}`

The Nuxt runtime expects that private listener behind its configured API
boundary. No provider credential is sent to the browser.

## Plans and audit data

`/admin/plans` and `/admin/audit` read dedicated server-side projections
instead of the small dashboard aggregates. Both lists apply filters,
ordering, and pagination in PostgreSQL; the current values remain in the URL
query string so a protected view can be bookmarked or shared with another
authorized administrator.

Plan filters cover the public plan, billing state, public/internal type,
workspace or owner, and an inclusive UTC update interval. Every row includes
the workspace and owner, billing state, relevant dates, and current
member/channel/scheduled-publication usage. Internal rows retain the real
used values and mark capacity as unlimited. The assign/revoke dialog still
uses the existing F11 boundary with server allowlist, CSRF, recent
re-authentication, explicit confirmation, required reason, idempotency, and
immutable audit checks.

Audit filters cover inclusive UTC occurrence interval, action, actor,
subject, and outcome. The detail endpoint returns only the same allowlisted
immutable projection shown in the list: event ID, time, action, actor,
subject, outcome, reason, and correlation ID. It never returns a request
payload, token, secret, or provider data.

CSV and XLSX exports always re-run authorization and export the entire
filtered result rather than the visible page. Both formats have a hard limit
of 10,000 rows and an allowlisted column set. Spreadsheet-formula prefixes
are neutralized before serialization; exceeding the limit returns
`413 ADMIN_EXPORT_LIMIT_EXCEEDED` so the administrator can narrow the
filters.

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

## User and workspace directories

The two directory pages load the first page immediately; a text search is
optional and never gates access to the complete list. Filter state, page size,
page, sort column, and direction are encoded in the URL query string so a view
can be bookmarked or shared with another authorized administrator. Each
navigation action requests only the selected page from the server.

User filters cover account status (`active` or currently password-`locked`),
email verification, public/internal plan, password or OAuth login method,
registration interval, and last-login interval. The safe user projection
contains email, display name, verification, method names, timestamps, active
session count, and active workspace membership with role and plan. It never
contains password hashes, session tokens, OAuth tokens, provider subjects, or
payment identifiers. `GET /users/{account_id}` applies the same authorization
boundary to a dedicated safe drill-down.

Workspace filters cover status, public/internal plan, owner name/email, created
interval, and updated interval. The projection contains owner identity,
billing state, active member count, non-revoked social-connection count,
scheduled-post count, and creation/update timestamps. Counts are computed by
PostgreSQL and are never obtained by loading another feature's complete
dataset into the browser.

All date filters use UTC calendar dates. `*_from` is inclusive and `*_to` is
inclusive through the end of that UTC date. Supported page sizes are
`10`, `25`, `50`, and `100`; defaults are page `1`, size `25`,
`registered_at desc` for users, and `updated_at desc` for workspaces. Invalid
filters, ranges, sort keys, or page values return
`400 ADMIN_INVALID_FILTERS`.

## Directory export

CSV and XLSX exports run server-side after the same administrator check and
reapply the exact directory filters and ordering. They include every filtered
row, not just the visible page. Exported fields are the same safe fields shown
by the directory. CSV is UTF-8 with a BOM; XLSX is an OOXML workbook with
inline string cells. Values beginning with `=`, `+`, `-`, `@`, tab, or carriage
return are prefixed with an apostrophe in both formats to mitigate spreadsheet
formula injection.

Exports are synchronous and limited to **10,000 rows**. Larger result sets
return `413 ADMIN_EXPORT_LIMIT_EXCEEDED`; the administrator must narrow the
filters and retry. Attachment names are generated only from the fixed
`postqron-admin-{users|workspaces}` prefix, the server UTC date, and the
selected extension. Responses use `no-store`, `nosniff`, and the appropriate
CSV or XLSX content type.

## Tests

```sh
pnpm --dir features/f31-admin-console test
GOWORK=off go test -race ./...
pnpm --dir features/f31-admin-console typecheck
```
