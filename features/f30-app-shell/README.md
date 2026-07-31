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
  F30 exposes only `instance_origin` for Mastodon and
  only `handle`, `did`, or `pds_origin` for Bluesky, and rejects every
  provider-incompatible input. Availability is rendered exclusively from the
  authoritative F5 bootstrap; F30 never accepts passwords/app-passwords or
  models nonce/DPoP secrets.
  For deployments with separate app and API origins, providers redirect to the
  public `/app/social-oauth/callback` relay registered by this feature. The
  relay strips OAuth response parameters from browser history and forwards only
  `state`, `code`, `error`, and optional `iss` to the fixed authoritative F5
  callback via credentialed CORS. F5 remains responsible for one-time state,
  issuer, PKCE, and provider validation. Only the client-safe resource
  selection/error envelope is handed to the parent; token material is never
  placed in the DOM, browser storage, or URL. Before external navigation F30 sets the popup's
  `opener` to `null`, preventing the provider from navigating the app while the
  parent retains its polling handle. The relay has no configurable upstream and
  cannot operate as an open proxy.

  OAuth start does not accept or send a client-selected `redirect_uri`. The
  provider authorization URL and the subsequent code exchange use the exact
  redirect configured by the authoritative F5 adapter through `RUNTIME_ENV`.
  Consequently, every enabled adapter must use this canonical, unlocalized
  callback:

  ```text
  https://APP_DOMAIN/app/social-oauth/callback
  ```

  The same exact URI (scheme, host, path, and trailing-slash behavior) must be
  registered in each provider developer console. For the current production
  topology, where `APP_DOMAIN=postqron.com` and
  `API_DOMAIN=api.postqron.com`, the concrete registered callback must be
  `https://postqron.com/app/social-oauth/callback`. Every configured value
  below must equal it:

  - `POSTQRON_F05_FACEBOOK_REDIRECT_URL`
  - `POSTQRON_F05_INSTAGRAM_REDIRECT_URL`
  - `POSTQRON_F05_X_REDIRECT_URL`
  - `POSTQRON_F05_LINKEDIN_REDIRECT_URL`
  - `POSTQRON_F05_PINTEREST_REDIRECT_URL`
  - `POSTQRON_F05_TIKTOK_REDIRECT_URL`
  - `POSTQRON_F05_YOUTUBE_REDIRECT_URL`
  - `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL`
  - `POSTQRON_F05_THREADS_REDIRECT_URL`
  - `POSTQRON_F05_MASTODON_REDIRECT_URL`
  - `POSTQRON_F05_BLUESKY_REDIRECT_URL`

  The versioned deploy workflow copies the opaque GitHub Environment secret
  `RUNTIME_ENV` into production without deriving or validating these values.
  Repository and GitHub metadata prove that the two production domains are
  separate and that the secret exists, but cannot prove its contents or the
  external provider registrations. Until an authorized operator verifies both
  the production `RUNTIME_ENV` values and every corresponding provider-console
  registration against the canonical callback above, OAuth is not demonstrated
  operational in production. That verification is an explicit merge/deploy
  prerequisite; F30 must not claim the OAuth P1 resolved from fixtures alone.
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

The current F7 contract exposes an aggregate post status and channel IDs, not a
provider-specific per-destination publishing result or stored failure cause.
F30 renders the authoritative aggregate state for every destination and uses a
real F5 reconnect reason when one exists; it does not fabricate F8 diagnostics.
Create and duplicate attach a fresh browser-safe `Idempotency-Key` for each
user intent. Ambiguous retries reuse the same key and exact payload so F7 can
replay its immutable response; completed, rejected, and changed-payload intents
receive a new key.

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
