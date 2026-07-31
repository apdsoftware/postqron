# TikTok connection adapter

Verified against the official TikTok documentation on 2026-07-30:

- [Login Kit for Web](https://developers.tiktok.com/doc/login-kit-web/)
- [User access token management](https://developers.tiktok.com/doc/oauth-user-access-token-management/)
- [Content Posting API: Get Started](https://developers.tiktok.com/doc/content-posting-api-get-started/)
- [Query Creator Info](https://developers.tiktok.com/doc/content-posting-api-reference-query-creator-info/)
- [Scopes reference](https://developers.tiktok.com/doc/tiktok-api-scopes/)

## OAuth and discovery contract

The adapter requests exactly one atomic scope entry, `video.publish`, serialized
with TikTok's documented comma separator. It uses the documented web
authorization endpoint, authorization-code exchange, 24-hour access token,
rotating refresh token, Creator Info discovery/verification, and token
revocation endpoint.

Creator Info is both the connection discovery endpoint and the verification
probe. Its documented `creator_username` is used as the remote profile
identifier and `creator_nickname` as the display name. The short-lived avatar
URL is deliberately not persisted.

TikTok's web Login Kit documentation does not specify PKCE, so this adapter
does not send a challenge or verifier. F5's cryptographically random, one-time
state remains mandatory and is consumed before denial or code handling.

## Availability gates

TikTok remains fail-closed until every gate is exact `true`:

| Gate | Runtime variable | Client-safe state when incomplete |
| --- | --- | --- |
| Provider enablement | `POSTQRON_F05_TIKTOK_ENABLED` | `not_configured` |
| Complete client key, secret, and exact HTTPS redirect | `POSTQRON_F05_TIKTOK_CLIENT_KEY`, `POSTQRON_F05_TIKTOK_CLIENT_SECRET`, `POSTQRON_F05_TIKTOK_REDIRECT_URL` | `not_configured` |
| Content Posting product and `video.publish` review | `POSTQRON_F05_TIKTOK_CONTENT_POSTING_REVIEW_APPROVED` | `review_required` |
| Direct Post audit | `POSTQRON_F05_TIKTOK_CONTENT_POSTING_AUDIT_APPROVED` | `audit_required` |
| Environment smoke | `POSTQRON_F05_TIKTOK_RUNTIME_SMOKE_VERIFIED` | `audit_required` |
| Active-user cap and provider access confirmed | `POSTQRON_F05_TIKTOK_QUOTA_ACCESS_CONFIRMED` | `review_required` |

The shared `POSTQRON_F05_ENABLED` cipher gate and valid external AES-256 key
remain prerequisites. No gate response identifies a missing secret.

TikTok documents that unaudited clients restrict posts to private visibility,
Creator Info is limited to 20 requests per minute per access token, and active
publishing-user caps can block the API. The runtime therefore cannot become
`available` merely because credentials exist.

## Token and failure handling

Access and refresh tokens are returned only to the F5 service, encrypted by the
central workspace/provider/resource-bound cipher, and never serialized to the
browser or logs. Refresh-token rotation is preserved. Revocation sends the
access token only in an HTTPS form body to TikTok's documented revoke endpoint.

Offline fixtures cover authorization denial, exchange, discovery, refresh,
revocation, incomplete grants, active-user quota, rate limiting, processing
errors, HTTP 401/403/404/429/5xx, and malformed payloads. Provider messages and
log identifiers stay server-side; clients receive only the existing safe F5
error categories.
