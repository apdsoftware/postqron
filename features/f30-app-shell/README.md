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
- **Social channels** (`/app/social-channels`) renders the complete D2 provider
  catalog from the F5 0.3.0 contract (#302 / PRs #315 and #321), including
  resource and publishing-mode capabilities plus `ready`, `not_configured`,
  `review_required`, and `audit_required` states. It consumes client-safe
  availability, connection listing, OAuth start, callback resource selection,
  reconnection, and revocation through `core/social-connections.ts`
  (parsers) and `core/social-api.ts` (client). It never models or renders token
  material, reads the flat `{code,message,retryable}` F5 error envelope, treats
  every undeclared code and unavailable provider as fail-closed, and enforces
  the F10 channel quota server-side (surfacing `channel_quota_exceeded` /
  `channel_quota_unavailable`). Owner-only mutations rely on the F5 exact
  `Origin` allowlist configured through `POSTQRON_AUTH_ALLOWED_ORIGINS`; F5
  serves credentialed CORS and public preflight handlers, so no CSRF token is
  exchanged.
  Provider bootstrap `unavailable` is explicit and never treated as offline.
  #324 is CLOSED and delivered the authoritative dynamic-discovery/PAR/DPoP
  contract needed by #314. F30 exposes only `instance_origin` for Mastodon and
  only `handle`, `did`, or `pds_origin` for Bluesky, and rejects every
  provider-incompatible input. The providers remain visible but unavailable
  until the still-unintegrated #353 and #328 adapter work is present; F30 never
  accepts passwords/app-passwords or models nonce/DPoP secrets.
  F5 returns the selection JSON directly from
  `/api/v1/social-authorizations/callback`. Popup polling is therefore allowed
  only when that API callback is exposed through the app origin (normally by a
  trusted reverse proxy). Before external navigation F30 sets the popup's
  `opener` to `null`, preventing the provider from navigating the app while the
  parent retains its polling handle. If `APP_DOMAIN` and `API_DOMAIN` expose
  different browser origins, F30 fails closed before starting OAuth: F5 has no
  authoritative app redirect, relay token, or `postMessage` handoff contract,
  and inventing one in this slice would be unsafe. A cross-origin relay must be
  formalized separately in F5 before that deployment topology can connect a
  channel.
- **Publish** (`/app/publish`) consumes only F6 0.2.0 (#303 / PR #317) for the
  capability catalog, drafts, optimistic autosave, validation, and secure media
  upload. Destination formats and provider metadata fields are rendered from
  `ContentCapability`; no provider limits or validation rules are duplicated.
  `Publish now` sends a two-minute future wall-clock time to F7 so the existing
  scheduling contract can enqueue it safely, including the offset of the exact
  target instant during a repeated DST hour. Thread constraints are intersected
  fail-closed across every selected destination; stale thread data is never
  submitted and remains removable. When editing, the same scheduled post is
  always rescheduled and a second post is never created.
- **Calendar** (`/app/calendar`) consumes only F7 0.2.0 (#304 / PR #308) for
  half-open calendar queries, edit, duplicate, reschedule, and cancellation.
  Times use an explicit IANA selection generated by
  `Intl.supportedValuesOf('timeZone')` with safe `UTC`, detected-zone, and
  `Europe/Rome` fallbacks. Unique wall times always receive a freshly computed
  offset; repeated wall times require an explicit occurrence, and nonexistent
  wall times are rejected. The initial calendar month follows the display
  timezone rather than the UTC month.

The F30 UI remains blocked on the still-unintegrated #308 and #317 scheduling
and composer dependencies. #353 and #328 remain explicit dependencies for the
dynamic-provider adapters; #324 is no longer a blocker because it is CLOSED
and delivered.

The current F7 contract exposes an aggregate post status and channel IDs, not a
provider-specific per-destination publishing result or stored failure cause.
F30 renders the authoritative aggregate state for every destination and uses a
real F5 reconnect reason when one exists; it does not fabricate F8 diagnostics.
Create/edit/duplicate may return `scheduling_dependency_unavailable` until the
F5/F6 validation and immutable-revision boundary documented by PR #308 is fully
integrated.

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
pnpm --dir features/f30-app-shell test:playwright
pnpm --filter @postqron/web typecheck
pnpm --filter @postqron/web build
```

The existing feature-package CI collector runs `pnpm test`. On CI the F30 test
runner installs the pinned launch-readiness Playwright package and Chromium,
then executes `test/playwright.config.ts`; local `pnpm test` remains the fast
unit suite, while `test:playwright` opts into the real-browser run.
