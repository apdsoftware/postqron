# F30 — Product app shell

This slice owns the real `/app` entry point, the launch email/password sign-in
and registration UI, onboarding UI, server-session route guard, responsive
product chrome, application states, and declarative extension slots. It does
not own pricing, checkout, admin, or any vertical feature.

## Runtime boundaries

- Authentication uses the F3 HTTP contract and its secure, HttpOnly
  `__Host-postqron_session` cookie. Private routes always resolve the session
  through `GET /api/v1/app/session`; the client never treats a profile payload
  or query parameter as proof of authentication.
- The launch UI is password-only. F30 does not render provider sign-in,
  linking, or unlinking controls, and the retired `/app/providers` destination
  redirects to Security instead of rendering an empty page. F3 OAuth/OIDC
  contracts and the callback compatibility route remain unchanged.
- Onboarding delegates legal receipts to F3 and workspace creation/selection
  to F4 through the F30 façade described in
  `contracts/app-shell.openapi.yaml`.
- Welcome, workspace invitation, onboarding, and security notifications are
  F14 transactional commands. The shell contains no Mailronix, SMTP, or email
  provider client.
- F36 owns locale resolution and redirect behavior. This slice only registers
  its complete `appShell` catalog and carries the locale in local return URLs.
- Account deletion obtains the F12 cancellation capability immediately before
  the authenticated deletion request. The capability remains exclusively in
  an HttpOnly cookie; F30 accepts only `expires_at`, then moves to the public
  `/app/account-deletions/{id}/cancel` route. That route does not resolve a
  session or account-area payload and posts the non-secret deletion ID with
  cookie credentials only. Workspace deletion keeps the authenticated F12
  cancellation flow.
- The public purchase intent accepts only `start|pro|team`,
  `monthly|annual`, and an integer quantity within the selected plan's public
  channel limit. Invalid or external destinations are rejected.
- **Social channels** (`/app/social-channels`) is a navigation entry for the
  Facebook Pages and Instagram Professional resources the workspace publishes
  to (D02); it is unrelated to account authentication. The page consumes only
  the F5 runtime contract (#282 / PR #287) — client-safe availability
  bootstrap, connection listing, OAuth start, callback resource selection,
  reconnection, and revocation — through `core/social-connections.ts`
  (parsers) and `core/social-api.ts` (client). It never models or renders token
  material, reads the flat `{code,message,retryable}` F5 error envelope, treats
  every undeclared code and unavailable provider as fail-closed, and enforces
  the F10 channel quota server-side (surfacing `channel_quota_exceeded` /
  `channel_quota_unavailable`). Owner-only mutations rely on the F5 exact
  `Origin` allowlist configured through `POSTQRON_AUTH_ALLOWED_ORIGINS`; F5
  serves credentialed CORS and public preflight handlers, so no CSRF token is
  exchanged.
  Provider bootstrap `unavailable` is rendered as explicit missing Meta
  configuration, while `provider_access_denied` and `provider_temporary`
  produce distinct permission and retryable upstream-error messages.

The F30 façade is deliberately a backend-for-frontend contract. Until its
server adapter is configured, `/app` still renders a retryable configuration
state instead of returning 404 or leaking provider details.

## Extension slots

The shell exposes semantic DOM anchors through `data-postqron-slot`:

- `primary-navigation`
- `workspace-actions`
- `home-summary`
- `home-primary`
- `home-secondary`
- `feature-content`

Feature slices can target these stable anchors from their own runtime plugin or
route without importing the shell and without editing a central registry.
Unknown feature routes render the accessible empty state and the
`feature-content` anchor.

## Tests

```sh
pnpm --dir features/f30-app-shell test
pnpm --filter @postqron/web build
```
