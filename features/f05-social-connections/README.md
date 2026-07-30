# F5 — Social connections

This slice connects the launch channels selected by D2:

- Facebook Pages managed with the official Meta Graph API;
- Instagram Professional Business or Creator accounts through Instagram Login.

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

- a client-safe provider bootstrap;
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

Provider availability is fail-closed. A provider is reported as `available`
only when the global flag is exactly `true`, its App Review flag is exactly
`true`, its complete Meta configuration is valid, and the external encryption
key is a valid 32-byte key. Otherwise bootstrap reports `unavailable`, OAuth
returns a non-retryable `provider_unavailable`, and listing/local revocation remain
usable. No client response distinguishes a missing secret from an incomplete
review.

Provider failures use client-safe categories: `provider_access_denied` for
authentication or permission rejection, `provider_temporary` for retryable
transport/rate-limit/5xx failures, `provider_resource_unavailable` for a lost
resource, and `provider_invalid_response` for malformed or unsupported
responses. Raw Meta error codes remain server-side diagnostics.

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
| `POSTQRON_F05_INSTAGRAM_CLIENT_ID` / `POSTQRON_F05_INSTAGRAM_CLIENT_SECRET` | Business Login for Instagram credentials. |
| `POSTQRON_F05_INSTAGRAM_REDIRECT_URL` | Exact server callback URL. |
| `POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED` | Exact `true` only after required review/Advanced Access. |

Values must be injected by the runtime secret store. Do not commit them, expose
them through bootstrap, or place them in browser configuration.

The Facebook adapter requests exactly `pages_show_list`,
`pages_read_engagement`, and `pages_manage_posts`, and accepts only Pages with
the `CREATE_CONTENT` task. The Instagram adapter requests exactly
`instagram_business_basic` and `instagram_business_content_publish`, and
accepts only Business or Creator accounts.

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
