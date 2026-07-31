# Google Business Profile F5 adapter

Verified on 2026-07-30 against the official Google Business Profile and Google
OAuth documentation.

## Contract

- OAuth: server-side web authorization-code flow at
  `https://accounts.google.com/o/oauth2/v2/auth` and
  `https://oauth2.googleapis.com/token`.
- OAuth state: generated, stored as a digest, consumed once, and expired by the
  provider-agnostic F5 service.
- PKCE: not advertised because Google's official web-server flow does not
  document a PKCE parameter for this client type.
- Exact minimum scope:
  `https://www.googleapis.com/auth/business.manage`.
- Offline scheduled use: authorization sends `access_type=offline` and
  `prompt=consent`; the callback fails closed if Google does not return the
  refresh token required by scheduled publishing.
- Account discovery: paginated
  `GET https://mybusinessaccountmanagement.googleapis.com/v1/accounts`.
- Location discovery: for every accessible account, paginated
  `GET https://mybusinessbusinessinformation.googleapis.com/v1/accounts/{id}/locations`
  with the required `readMask=name,title,storefrontAddress`.
- The selected remote resource preserves both identifiers as
  `accounts/{accountId}/locations/{locationId}`, which is required by the
  official Local Posts publishing path.
- Refresh: supported through `POST https://oauth2.googleapis.com/token`; an
  omitted rotated refresh token preserves the existing encrypted refresh
  token.
- Revocation: Google's endpoint revokes every scope granted to the Cloud
  project and invalidates sibling locations. Because F5 disconnect is
  per-resource, the adapter deliberately does not advertise remote revocation;
  it guarantees local credential deletion without breaking other selected
  locations.

Google Business Profile is `available` only with complete credentials, GBP API
access approval, OAuth review approval, separate runtime audit and smoke
approvals, and the shared F5 cipher. Missing inputs fail closed as
`not_configured`, `review_required`, or `audit_required`. The
provider-independent cipher bootstrap dependency is supplied by #321 for #318
without duplication here.

## Runtime keys

| Variable | Purpose |
| --- | --- |
| `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED` | Exact `true` opt-in. |
| `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_ID` / `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_SECRET` | Server-side OAuth web client credentials. |
| `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL` | Exact HTTPS callback URL. |
| `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_API_ACCESS_APPROVED` | Exact `true` after GBP project approval and non-zero quota. |
| `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_OAUTH_REVIEW_APPROVED` | Exact `true` after the required OAuth consent review. |
| `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RUNTIME_AUDIT_VERIFIED` | Exact `true` after the offline fixture and security audit. |
| `POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_SMOKE_VERIFIED` | Exact `true` only after environment smoke verification. |

All values come from the runtime secret store. Fixtures use non-routable
placeholder values and an in-memory HTTP transport; they make no live calls.

## Official sources

- [GBP prerequisites](https://developers.google.com/my-business/content/prereqs)
- [GBP accounts](https://developers.google.com/my-business/content/accounts)
- [Create Posts on Google](https://developers.google.com/my-business/content/posts-data)
- [GBP FAQ](https://developers.google.com/my-business/content/faq)
- [Account Management API discovery document](https://mybusinessaccountmanagement.googleapis.com/$discovery/rest?version=v1)
- [Business Information API discovery document](https://mybusinessbusinessinformation.googleapis.com/$discovery/rest?version=v1)
- [OAuth for web server applications](https://developers.google.com/identity/protocols/oauth2/web-server)
