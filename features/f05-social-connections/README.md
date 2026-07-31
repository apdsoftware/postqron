# F5 — Social connections

This slice provides the provider-agnostic connection platform for the channel
scope deliberated in #302:

- Facebook Pages managed with the official Meta Graph API;
- Instagram Professional Business or Creator accounts through Instagram Login.
- Facebook Groups and Instagram personal accounts in notification mode;
- X, LinkedIn profiles and Pages, Pinterest, TikTok, Google Business Profile,
  Mastodon, YouTube, Threads, and Bluesky.

Facebook Pages, Instagram Professional, and X currently have verified adapters
and complete offline provider fixtures. Every other provider is present only
in the versioned capability catalog and remains fail-closed with no
authorization, refresh, or revocation capability. Provider-family follow-up
issues own their official-documentation review, complete fixtures, and runtime
enablement. Pre-wired runtime extension files isolate those follow-ups by
family; setting an invented or premature environment flag cannot enable them.

These are publishing channels, not the Facebook identity/login providers owned
by F3. F5 never creates an application session or links a login method.

The domain flow is deliberately two-step. `Begin`/`Callback` discovers
publishable resources and returns only safe metadata; `Select` requires the
Owner to name one exact remote resource before a connection exists. OAuth
state is one-time, and PKCE is applied when the configured Meta flow supports
it.

`OAuthConfig.Scopes` always contains one entry per requested grant.
`ScopeSeparator` controls only serialization of the outbound OAuth `scope`
parameter: the zero value remains comma-delimited for exact compatibility with
the existing Meta adapters, while `OAuthScopeSeparatorSpace` supports the
individual scope sets used by X, LinkedIn, and Google.

`OAuthConfig.ClientParameterName` controls the public client identifier name in
the authorization request. Its zero value remains `client_id` for exact
compatibility with Meta, X, LinkedIn, Google, Pinterest, and other existing
adapters. TikTok selects `OAuthClientParameterClientKey` and therefore emits
only `client_key`. Both names are reserved: adapters cannot duplicate or
override them through the authorization endpoint query or `ExtraParameters`.
Client secrets remain server-side and are never part of this contract.

Access and refresh tokens are AES-256-GCM ciphertexts with random nonces,
external key identifiers, and workspace/provider/resource-bound additional
authenticated data. Plaintext tokens are never returned by list operations,
events, API contracts, or persistence models.

Dynamic OAuth providers use the optional `DynamicAdapter` contract. Public
`Begin` accepts only a typed discovery value (`instance_origin`, `handle`,
`did`, or `pds_origin`); reconnect never accepts a browser-supplied binding and
derives it from the existing connection. The adapter returns opaque attempt
and session state which F5 encrypts with issuer/resource/subject-bound AAD.
PAR `request_uri`, PKCE material, DPoP private keys, Authorization Server and
Resource Server nonces, and provider client credentials therefore remain
encrypted and are never serialized by the API.

Callback `iss`, discovered issuer/resource server (PDS), and token `sub` are
canonicalized and compared exactly. Bluesky configuration is rejected unless
it declares PAR, DPoP, issuer and subject binding, a single-use refresh-token
mode, redirect rejection, per-request DNS validation/dial pinning, and bounded
responses. Password and app-password variants do not exist in the contract.

`Service.AuthenticatedRequest` is an internal service-to-service boundary; it
has no HTTP route or OpenAPI operation. Callers provide only a relative path,
bounded body, and non-authentication headers. Absolute or scheme-relative
URLs, path traversal, encoded separators, and caller-controlled
`Authorization`, `DPoP`, `Host`, or `Cookie` headers are rejected before an
adapter is called. The adapter owns the bound resource-server origin, applies
its no-redirect/DNS-pinned transport, creates a fresh proof (including `ath`
for resource requests), and returns bounded response data without exposing
tokens, nonces, proofs, or private keys.

The LinkedIn DMS exception remains wholly inside F5. A dedicated initialize
operation derives the LinkedIn person/organization owner from the connection,
constructs and executes the exact relative API request itself, consumes the
provider response immediately, and returns only a random opaque handle. F5
stores the handle hash plus workspace/connection/provider binding, immutable
server-derived expiry, lifecycle state, and an AEAD-encrypted payload
containing the asset URN and exact signed URL/query. The generic executor
rejects the whole LinkedIn images endpoint family and every media-create
payload regardless of query, trailing slash, encoding, alternate path, or
method. The handle boundary alone may use the exact canonical initialize,
status, and `/rest/posts` create endpoints.

Upload, asset status, and guarded create accept only that handle. A persisted
CAS state machine uses expiring leases in the pre-call `uploading` and
`creating` states, then clears the lease and moves to non-replayable
`upload_sending` or `create_sending` immediately before the provider call.
Expired pre-call work is reclaimed with CAS; a restart after a provider call
returns an ambiguous result without replay. This prevents cross-workspace,
cross-connection, concurrent, restart, and reuse attacks. The fixed
`https://www.linkedin.com/dms-uploads/` origin is validated and DNS-pinned,
while the opaque query is preserved byte-for-byte. Upload accepts only media
`Content-Type`, derives the bearer inside F5, and streams a size-bounded
SHA-256-verified body. Status returns only a normalized state; guarded create
injects the encrypted asset URN server-side and requires `AVAILABLE`. No signed
URL/query, asset URN, origin, token, provider response, expiry control, or
session state crosses the F5 public boundary or appears in HTTP/OpenAPI.

Each dynamic request holds a persistent, random lease ID for one connection.
Nonce updates are saved under that lease, so concurrent or stale completions
cannot overwrite newer session state. A successful single-use refresh is
atomically persisted with the rotated refresh token and Authorization Server
nonce before the Resource Server request starts. If the process dies after the
refresh request may have consumed the token but before that commit, the
`session_refreshing` marker survives lease expiry; the old token is never
retried and the connection moves fail-closed to `reconnect_required`.

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

Providers may instead declare remote revocation as required; a remote failure
then leaves local credentials and quota intact.

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
`ready` only when its provider flag is exactly `true`, the relevant App Review
and runtime-audit flags are exactly `true`, its complete configuration is
valid, and the external encryption key is a valid 32-byte key. Listing and
local revocation remain usable when a provider is unavailable.

The shared credential cipher is gated by exact
`POSTQRON_F05_ENABLED=true`, independently of any provider family. For a safe
rolling migration, an absent provider-neutral gate falls back to exact
`POSTQRON_F05_META_ENABLED=true`; once `POSTQRON_F05_ENABLED` is present, its
value is authoritative. The legacy flag remains the independent Meta adapter
gate, so enabling the shared runtime cannot accidentally enable Meta.

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
| `POSTQRON_F05_ENABLED` | Provider-neutral cipher/runtime gate; only exact `true` enables shared credential encryption. When absent, the legacy Meta gate supplies a migration-safe fallback. |
| `POSTQRON_F05_META_ENABLED` | Meta adapter gate and legacy cipher fallback; only exact `true` enables Meta. |
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
| `POSTQRON_F05_X_ENABLED` | Exact `true` to request X runtime enablement; all remaining X gates must also pass. |
| `POSTQRON_F05_X_CLIENT_ID` / `POSTQRON_F05_X_CLIENT_SECRET` | Confidential X OAuth 2.0 web application credentials. |
| `POSTQRON_F05_X_REDIRECT_URL` | Exact registered HTTPS server callback URL. |
| `POSTQRON_F05_X_API_ACCESS_APPROVED` | Exact `true` only after developer app, write access, billing/access, and policy prerequisites are verified. |
| `POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED` | Exact `true` only after the offline fixture and security audit. |
| `POSTQRON_F05_X_SMOKE_TEST_VERIFIED` | Exact `true` only after an authorized environment smoke test. |

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

The X adapter, its exact official endpoints and scopes, the current atomic
scope-parameter compatibility workaround, refresh/revoke semantics, gates, and
offline fixtures are documented in [X.md](./X.md).

The dynamic contract is anchored to the official
[Mastodon OAuth documentation](https://docs.joinmastodon.org/spec/oauth/),
[AT Protocol OAuth profile](https://atproto.com/specs/oauth),
[RFC 9449 DPoP](https://datatracker.ietf.org/doc/html/rfc9449),
[RFC 9126 PAR](https://datatracker.ietf.org/doc/html/rfc9126),
[RFC 9207 issuer identification](https://datatracker.ietf.org/doc/html/rfc9207),
[RFC 8414 authorization-server metadata](https://datatracker.ietf.org/doc/html/rfc8414),
and [RFC 7009 revocation](https://datatracker.ietf.org/doc/html/rfc7009).

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
