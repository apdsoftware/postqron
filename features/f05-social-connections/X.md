# F5 — X adapter

The X adapter uses only the official X API v2 and OAuth 2.0 Authorization Code
flow with PKCE. It is a confidential server-side web client: the client secret
is used only through HTTP Basic authentication against the token and revocation
endpoints, while the browser receives only the authorization URL.

## OAuth and lifecycle

The adapter requests the minimum scopes needed by the current connection and
text-publishing capability:

- `tweet.read`
- `tweet.write`
- `users.read`
- `offline.access`

`offline.access` is required because X access tokens otherwise expire after two
hours and no refresh token is issued. Media upload is outside #310, so
`media.write` is not requested. Any future X media implementation must review
and version that scope change in its own F6/F8 work.

The provider-agnostic F5 authorization URL builder currently joins
`OAuthConfig.Scopes` with commas for Meta compatibility. X requires the OAuth
`scope` parameter to be space-delimited. Until the central builder gains a
provider-neutral delimiter contract, the X adapter supplies the exact
space-delimited value as one atomic `OAuthConfig.Scopes` entry. This is an
encoding workaround at the builder boundary only: every token response is
split with OAuth whitespace rules and validated as an individual, order-
independent set containing exactly the four scopes above. Missing, duplicate,
or additional scopes fail closed. Persisted scope metadata remains the
canonical atomic value so the current F5 refresh validator checks the same
grant without weakening validation. The provider-neutral replacement is
tracked in #318.

The server performs these official operations:

| F5 operation | Official X operation |
| --- | --- |
| Begin | `GET https://x.com/i/oauth2/authorize`, with one-time `state` and PKCE S256 |
| Callback exchange | `POST https://api.x.com/2/oauth2/token` with confidential-client Basic authentication |
| Discovery and verification | `GET https://api.x.com/2/users/me?user.fields=profile_image_url` |
| Refresh | `POST https://api.x.com/2/oauth2/token` with `grant_type=refresh_token` |
| Disconnect | `POST https://api.x.com/2/oauth2/revoke` for both access and refresh tokens |

Disconnect is idempotent. Both tokens are attempted even when one revocation
fails. An exact OAuth `invalid_token` response means that token is already in
the desired revoked state and does not block local encrypted credential
deletion. Authentication, permission, transport, rate-limit, and 5xx failures
that are not this exact already-revoked case remain classified and observable;
in particular, 429 and 5xx responses remain retryable provider failures. The
generic F5 service always performs local revocation and quota release after the
best-effort provider call, so plaintext-equivalent credential material cannot
remain usable locally.

## Fail-closed runtime gates

All values must come from the runtime secret/configuration store. No sample
below is a real credential.

| Variable | Required state |
| --- | --- |
| `POSTQRON_F05_X_ENABLED` | Exact `true` |
| `POSTQRON_F05_X_CLIENT_ID` | Confidential web app client ID |
| `POSTQRON_F05_X_CLIENT_SECRET` | Confidential web app client secret |
| `POSTQRON_F05_X_REDIRECT_URL` | Exact registered HTTPS server callback |
| `POSTQRON_F05_X_API_ACCESS_APPROVED` | Exact `true` only after the developer app, write access, billing/access prerequisites, and policy review are verified |
| `POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED` | Exact `true` only after fixture and security audit |
| `POSTQRON_F05_X_SMOKE_TEST_VERIFIED` | Exact `true` only after an authorized environment smoke test |

The shared F5 encryption key and global F5 runtime gate from the #302
foundation must also be valid. In the current foundation that global gate is
still named `POSTQRON_F05_META_ENABLED`; X therefore requires it to be exact
`true` until the central F5 bootstrap is made provider-neutral. This adapter
does not broaden that out-of-scope central contract; #318 tracks the central
follow-up. Missing credentials keep X `not_configured`; missing access approval
reports `review_required`; missing audit or smoke verification reports
`audit_required`. Only the complete set mounts the adapter and exposes real
authorization, PKCE, resource-selection, refresh, and remote-revocation
capabilities.

## Offline verification

Provider responses live under `testdata/x` and contain synthetic values only.
They cover authorization-code token exchange, reordered scope grants, user
discovery, refresh rotation, both-token revocation, already-invalid tokens,
401, 403, 429, 5xx, and malformed payloads. Tests use loopback HTTP servers and
never call X.

Official sources reviewed on 2026-07-30:

- [OAuth 2.0 Authorization Code Flow with PKCE](https://docs.x.com/fundamentals/authentication/oauth-2-0/authorization-code)
- [OAuth 2.0 user access token, refresh, and revoke flow](https://docs.x.com/fundamentals/authentication/oauth-2-0/user-access-token)
- [Get my User](https://docs.x.com/x-api/users/get-my-user)
- [Manage Posts integration guide](https://docs.x.com/x-api/posts/manage-tweets/integrate)
- [Response codes and errors](https://docs.x.com/x-api/fundamentals/response-codes-and-errors)
- [X API pricing and credits](https://docs.x.com/x-api/getting-started/pricing)
