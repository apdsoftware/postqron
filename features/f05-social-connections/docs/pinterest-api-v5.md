# Pinterest API v5 connection contract

Verified against the official Pinterest API documentation and the official
`pinterest/api-description` OpenAPI v5.28.0 on 2026-07-30.

## Authorization and tokens

- Authorization uses the server-side OAuth Authorization Code flow at
  `https://www.pinterest.com/oauth/`.
- The core owns the opaque, one-time `state` value and consumes it before code
  exchange. Pinterest's current flow does not document PKCE parameters, so the
  adapter does not claim PKCE support.
- Each scope remains an individual `OAuthConfig.Scopes` entry. The
  authorization parameter uses `OAuthScopeSeparatorSpace`; Pinterest documents
  both space and comma separators.
- The exact least-privilege set for board discovery and organic Pin creation is
  `boards:read`, `boards:write`, `pins:read`, and `pins:write`. Secret-board,
  ads, analytics, catalogs, and user-account scopes are not requested.
- Code exchange calls `POST https://api.pinterest.com/v5/oauth/token` with HTTP
  Basic authentication and the exact form fields `grant_type`,
  `code`, `redirect_uri`, and `continuous_refresh=true`.
- Refresh calls the same endpoint with HTTP Basic authentication and only
  `grant_type=refresh_token` plus the current `refresh_token`. The optional
  scope reduction parameter is omitted. Both the returned access token and the
  rotated continuous refresh token replace the previous encrypted credential.
- Access and refresh tokens never enter candidates, connections, client errors,
  URLs, or logs. The shared F5 cipher from #318/#321 seals both values with the
  existing workspace/provider/resource AAD before persistence.

## Board discovery and verification

- Discovery calls `GET /v5/boards` with `page_size=250` and follows the opaque
  `bookmark` until Pinterest returns null/empty.
- Repeated bookmarks, duplicate or malformed board IDs, missing `items`, and
  more than 100 pages fail closed.
- Ad-only, protected, and secret boards are not offered because their
  capabilities require permissions outside this organic least-privilege
  adapter.
- Discovery only creates selection candidates. The core requires the owner to
  submit the exact selection ID and board ID before a connection is stored.
- Verification calls `GET /v5/boards/{board_id}` and requires the same usable
  board ID to be returned.

## Revocation

The current official `POST /v5/oauth/token/revoke` description limits remote
revocation to tokens issued for system users. This adapter uses Authorization
Code user tokens and therefore returns
`ErrExternalRevocationUnavailable` without transmitting a token to that
endpoint. The core still revokes the Postqron connection, deletes both
ciphertexts, releases quota, and reports `provider_revoked=false`.

## Runtime gates

Pinterest becomes `available` only when all of the following server-side gates
pass:

- `POSTQRON_F05_ENABLED=true` and a valid provider-neutral shared cipher from
  #318/#321;
- `POSTQRON_F05_PINTEREST_ENABLED=true`;
- Pinterest client ID, client secret, and exact HTTPS redirect URL;
- `POSTQRON_F05_PINTEREST_ACCESS_APPROVED=true`;
- `POSTQRON_F05_PINTEREST_RUNTIME_AUDIT_VERIFIED=true`.

Missing credentials remain `not_configured`; missing access approval becomes
`review_required`; missing runtime audit becomes `audit_required`. No
Pinterest-specific fallback can activate the legacy Meta cipher gate.

## Official sources

- <https://developers.pinterest.com/docs/getting-started/set-up-authentication-and-authorization/>
- <https://developers.pinterest.com/docs/work-with-organic-content-and-users/create-boards-and-pins/>
- <https://developers.pinterest.com/docs/api/v5/boards-list/>
- <https://developers.pinterest.com/docs/reference/pagination/>
- <https://github.com/pinterest/api-description/blob/main/v5/openapi.yaml>

All contract tests use local `httptest` servers and synthetic fixtures. They
perform no live Pinterest calls and contain no production credential or token.
