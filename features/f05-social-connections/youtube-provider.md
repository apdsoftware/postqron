# YouTube connection adapter

Verified against the official Google and YouTube documentation on 2026-07-30:

- [OAuth 2.0 for web server applications](https://developers.google.com/youtube/v3/guides/auth/server-side-web-apps)
- [OAuth 2.0 scopes for Google APIs](https://developers.google.com/identity/protocols/oauth2/scopes)
- [Channels: list](https://developers.google.com/youtube/v3/docs/channels/list)
- [Videos: insert](https://developers.google.com/youtube/v3/docs/videos/insert)

## OAuth and channel discovery contract

The adapter requests these two atomic scope entries, serialized with Google's
documented space separator:

1. `https://www.googleapis.com/auth/youtube.readonly`
2. `https://www.googleapis.com/auth/youtube.upload`

`youtube.upload` is documented as managing YouTube videos and is required by
`videos.insert`. `channels.list?mine=true` requires OAuth, but its method
reference does not explicitly say that `youtube.upload` grants permission to
view the authenticated account. The official scope catalog describes
`youtube.readonly` as viewing the YouTube account. The adapter therefore does
not infer an undocumented overlap: both grants are required, and a token
response missing either one is rejected before channel discovery.

The authorization request uses `access_type=offline` and `prompt=consent` so a
server-side refresh token is issued for scheduled work and reconnects. It does
not enable incremental authorization, which avoids silently adding old scopes
to this minimal request. Discovery and verification call
`GET /youtube/v3/channels?part=id,snippet&mine=true`.

Google's current server-side web application guide does not specify PKCE for
this flow. The adapter therefore does not send a challenge or verifier. F5's
cryptographically random, one-time state remains mandatory.

## Availability gates

YouTube remains fail-closed until every gate is exact `true`:

| Gate | Runtime variable | Client-safe state when incomplete |
| --- | --- | --- |
| Provider enablement | `POSTQRON_F05_YOUTUBE_ENABLED` | `not_configured` |
| Complete OAuth client ID, secret, and exact HTTPS redirect | `POSTQRON_F05_YOUTUBE_CLIENT_ID`, `POSTQRON_F05_YOUTUBE_CLIENT_SECRET`, `POSTQRON_F05_YOUTUBE_REDIRECT_URL` | `not_configured` |
| OAuth consent-screen verification | `POSTQRON_F05_YOUTUBE_OAUTH_VERIFICATION_APPROVED` | `review_required` |
| YouTube upload API audit | `POSTQRON_F05_YOUTUBE_API_AUDIT_APPROVED` | `audit_required` |
| Environment smoke | `POSTQRON_F05_YOUTUBE_RUNTIME_SMOKE_VERIFIED` | `audit_required` |
| YouTube Data API access and quota confirmed | `POSTQRON_F05_YOUTUBE_QUOTA_ACCESS_CONFIRMED` | `review_required` |

The shared `POSTQRON_F05_ENABLED` cipher gate and valid external AES-256 key
remain prerequisites. No client response identifies a missing credential.

The `videos.insert` reference documents that uploads from unverified projects
created after 2020-07-28 are private until the project passes an audit. Current
quota documentation also gives video insertion its own upload quota bucket.
Credentials alone therefore never make this adapter available.

## Token, revocation, and failure handling

Access and refresh tokens remain server-side and are encrypted by the central
workspace/provider/resource-bound cipher. Google refresh responses may omit a
replacement refresh token; in that case the encrypted current refresh token is
retained. A returned scope set is validated atomically before persistence.

Google documents revocation at `https://oauth2.googleapis.com/revoke`. The
adapter revokes the refresh token when present, otherwise the access token.
This invalidates the Google project grant, so remote revocation is attempted
only for an explicit F5 disconnect; local encrypted credentials are deleted
regardless of provider availability.

Offline fixtures cover authorization denial, token exchange, channel
discovery, refresh, revocation, partial grants, quota and rate limits,
processing errors, HTTP 401/403/404/429/5xx, and malformed payloads. Raw Google
messages remain server-side; clients receive only the existing safe F5 error
categories.
