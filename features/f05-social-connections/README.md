# F5 — Social connections

This slice provides the provider-agnostic connection platform for the channel
scope deliberated in #302:

- Facebook Pages managed with the official Meta Graph API;
- Instagram Professional Business or Creator accounts through Instagram Login.
- Facebook Groups and Instagram personal accounts in notification mode;
- X, LinkedIn profiles and Pages, Pinterest, TikTok, Google Business Profile,
  Mastodon, YouTube, Threads, and Bluesky.

Only the first two entries currently have verified adapters and complete
offline provider fixtures. Every other provider is present only in the
versioned capability catalog and remains fail-closed with no authorization,
refresh, or revocation capability. Provider-family follow-up issues own their
official-documentation review, complete fixtures, and runtime enablement.
Pre-wired no-op runtime extension files isolate those follow-ups by family;
setting an invented or premature environment flag cannot enable them.

These are publishing channels, not the Facebook identity/login providers owned
by F3. F5 never creates an application session or links a login method.

The domain flow is deliberately two-step. `Begin`/`Callback` discovers
publishable resources and returns only safe metadata; `Select` requires the
Owner to name one exact remote resource before a connection exists. OAuth
state is one-time, and PKCE is applied when the configured Meta flow supports
it.

Access and refresh tokens are AES-256-GCM ciphertexts with random nonces,
external key identifiers, and workspace/provider/resource-bound additional
authenticated data. Plaintext tokens are never returned by list operations,
events, API contracts, or persistence models.

Only `channels.manage` may start, select, reconnect, or revoke a channel.
Workspace members with `workspace.view` can list safe connection state. Every
state change and token refresh writes an outbox event in the same repository
transaction as the connection change.

The authenticated runtime exposes:

- a client-safe provider bootstrap with catalog version, resource types,
  publishing modes, runtime capabilities, and configuration state;
- connection listing;
- OAuth start and a one-time public callback bound to the initiating workspace
  and actor through the stored state;
- explicit resource selection;
- explicit reconnect start;
- local revocation, plus provider revocation when it is safe for the grant.

All private routes read the account established by the shared API session
middleware. The callback does not trust a browser identity: it consumes the
single-use, expiring state created by an Owner-authorized request.

Browser access is credentialed CORS with an exact, fail-closed origin
allowlist read from `POSTQRON_AUTH_ALLOWED_ORIGINS`. Public `OPTIONS` handlers
cover every F5 route so preflight is never intercepted by session
authentication. Allowed-origin headers are also emitted on callback and error
responses. Every mutation requires exactly one syntactically valid `Origin`
whose normalized HTTP(S) origin is in the allowlist; `Sec-Fetch-Site` is not
used as authorization evidence. Provider callback navigation without an
`Origin` remains valid, while a browser request that supplies an absent,
invalid, or unlisted origin never receives CORS permission.

An authentication, missing-scope, or lost-resource response transitions the
connection once to `reconnect_required`, clears stored credentials, and emits
one event. Later automatic access attempts fail locally without calling Meta,
preventing infinite refresh/publish loops. A new explicit OAuth selection for
the same resource restores the existing connection as `connected`.

## Runtime configuration

`NewPostgresModule` is discovered by the feature bundler from the server routes
in `feature.yaml`. It uses the shared PostgreSQL runtime for F4 authorization,
F10 channel quota commands, F5 persistence, and the authenticated account
context.

Provider availability is fail-closed. The binary `status` is `available` only
when a verified adapter is mounted. The client-safe `configuration_state`
distinguishes `not_configured`, `review_required`, `audit_required`, and
`ready`; it never identifies which credential or secret is absent. Meta is
`ready` only when the global flag is exactly `true`, the relevant App Review
and runtime-audit flags are exactly `true`, its complete configuration is
valid, and the external encryption key is a valid 32-byte key. Listing and
local revocation remain usable when a provider is unavailable.

`providers` remains a two-entry Meta compatibility projection for the F30
client shipped before #302. New clients consume `catalog`, whose version is
`2026-07-30`. This prevents a foundation-only F5 deployment from breaking the
existing Social channels page while #306 is still pending.

Provider failures use client-safe categories: `provider_access_denied` for
authentication or permission rejection, `provider_temporary` for retryable
transport/rate-limit/5xx failures, `provider_resource_unavailable` for a lost
resource, and `provider_invalid_response` for malformed or unsupported
responses. Availability uses `provider_not_configured`,
`provider_review_required`, `provider_audit_required`, or the fallback
`provider_unavailable`; reconnect state uses `reconnect_required`. Raw provider
codes remain server-side diagnostics.

Runtime secret-store/environment keys:

| Variable | Purpose |
| --- | --- |
| `POSTQRON_AUTH_ALLOWED_ORIGINS` | Comma-separated exact web origins allowed to make credentialed F5 requests, for example `https://postqron.com`; invalid or unlisted origins fail closed. |
| `POSTQRON_F05_META_ENABLED` | Global fail-closed flag; only exact `true` enables adapters. |
| `POSTQRON_F05_META_GRAPH_VERSION` | Explicit supported version such as `v25.0`. |
| `POSTQRON_F05_CIPHER_KEY_ID` | External encryption-key identifier. |
| `POSTQRON_F05_CIPHER_KEY_BASE64` | Base64-encoded 32-byte AES key. |
| `POSTQRON_F05_FACEBOOK_CLIENT_ID` / `POSTQRON_F05_FACEBOOK_CLIENT_SECRET` | Facebook Login for Business application credentials. |
| `POSTQRON_F05_FACEBOOK_REDIRECT_URL` | Exact server callback URL. |
| `POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID` | Reviewed Facebook Login configuration. |
| `POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED` | Exact `true` only after required review/Advanced Access. |
| `POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED` | Exact `true` only after a fixture-complete runtime audit and environment smoke test. |
| `POSTQRON_F05_INSTAGRAM_CLIENT_ID` / `POSTQRON_F05_INSTAGRAM_CLIENT_SECRET` | Business Login for Instagram credentials. |
| `POSTQRON_F05_INSTAGRAM_REDIRECT_URL` | Exact server callback URL. |
| `POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED` | Exact `true` only after required review/Advanced Access. |
| `POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED` | Exact `true` only after a fixture-complete runtime audit and environment smoke test. |

Values must be injected by the runtime secret store. Do not commit them, expose
them through bootstrap, or place them in browser configuration.

The Facebook adapter requests exactly `pages_show_list`,
`pages_read_engagement`, and `pages_manage_posts`, and accepts only Pages with
the `CREATE_CONTENT` task. The Instagram adapter requests exactly
`instagram_business_basic` and `instagram_business_content_publish`, and
accepts only Business or Creator accounts.

The preserved Meta adapter and fixtures are anchored to the official
[Pages API](https://developers.facebook.com/docs/pages-api/),
[Facebook Login for Business](https://developers.facebook.com/docs/facebook-login/facebook-login-for-business/),
and [Instagram API with Instagram Login](https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/)
documentation. The runtime requires an explicit Graph version so a provider
version change cannot happen silently.

Before a new resource (or a previously revoked resource) is persisted, the
service sends an atomic `channels +1` command to F10 using a server-generated
idempotency key. Reconnecting a `reconnect_required` channel does not consume a
second unit. Revocation sends the matching server-side `channels -1` command;
quota resource, delta, timestamps, and usage are never accepted from the
browser.

## Verification

```sh
cd features/f05-social-connections
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Set `F05_DATABASE_URL` to run the optional PostgreSQL integration test when a
database with the slice migration is available.
