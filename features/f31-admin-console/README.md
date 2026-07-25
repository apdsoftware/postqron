# F31 — Protected admin console

This slice owns `/admin`, its five-locale catalog, the private admin HTTP
adapter, and the security policy applied to every admin operation.

## Security boundary

The HTTP adapter must be mounted only on the authenticated private channel
provided by F24. It intentionally is not declared as a public server route in
`feature.yaml`. `Handler` authenticates every request and returns `403` before
reading admin data when the verified, normalized session email is not both
active in the admin directory and present in the server allowlist.

The initial production configuration is:

```sh
POSTQRON_ADMIN_ALLOWLIST=carlo.zuffetti@apdsoftware.it
```

The address is configuration, not a password or an authentication bypass.
Access also requires a valid OAuth session, verified email, an active
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
`POSTQRON_ADMIN_ALLOWLIST`, calls `BootstrapAdmins` during startup, and exposes
`NewHandler(service, authenticator)` only through F24's authenticated private
route overlay. The hybrid manifest declares every adapter route as `private`;
the generic public `/api/v1/` fallback never receives those patterns.
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
