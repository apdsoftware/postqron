# F31 — Protected admin console

This slice owns `/admin` and its dedicated sections (`/admin/users`,
`/admin/workspaces`, `/admin/plans`, `/admin/audit`, `/admin/profile`), its
five-locale catalog, the private admin HTTP adapter, and the security policy
applied to every admin operation.

## Shell

`layouts/admin-console.vue` renders a persistent desktop sidebar that becomes
a keyboard- and click-dismissible drawer under 48rem, a sticky top bar with
the current section, a `<select>`-based language switcher
(`components/AdminLanguageSelect.vue`, matching the public site's `<select>`
pattern rather than a row of links), the authenticated administrator's email
linking to `/admin/profile`, and a predisposed
`data-postqron-slot="admin-logout-action"` position for a future sign-out
control. Every route in `feature.yaml` declares the same `admin-console`
layout and `admin-access` middleware, and `components/nav.ts` is the single
source of truth for sidebar entries, active-state matching, and Dashboard
quick links.

Reusable page building blocks live in `components/`: `AdminPageHeader`
(eyebrow/title/description), `AdminKpiCard`, `AdminAlert`, `AdminDataTable`,
`AdminFilterBar`, `AdminPagination`, `AdminState` (loading/empty/error), and
`AdminLoginGate` (the shared email/password sign-in form). This issue ships
structure, loading, and empty states only; full data views land in dependent
issues.

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

## Tests

```sh
pnpm --dir features/f31-admin-console test
GOWORK=off go test -race ./...
pnpm --dir features/f31-admin-console typecheck
```
