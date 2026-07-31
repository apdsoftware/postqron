# LinkedIn F5 adapter

Verified on 2026-07-30 against LinkedIn's official documentation.

## Contract

- OAuth: server-side three-legged authorization code flow at
  `https://www.linkedin.com/oauth/v2/authorization` and
  `https://www.linkedin.com/oauth/v2/accessToken`.
- OAuth state: generated, stored as a digest, consumed once, and expired by the
  provider-agnostic F5 service.
- PKCE: not advertised because LinkedIn's official web authorization-code
  documentation does not document PKCE for this flow.
- Exact least-privilege scope union for the two selectable resource types:
  `openid profile w_member_social rw_organization_admin
  w_organization_social`. `r_member_social` and
  `r_organization_social` are not requested.
- Member discovery: `GET /v2/userinfo`; the selected resource is normalized to
  `urn:li:person:{sub}`.
- Page discovery: paginated `GET /rest/organizationAcls?q=roleAssignee` with
  approved publishing roles, followed by versioned
  `GET /rest/organizations/{id}` lookups. Analyst and revoked/requested roles
  are rejected.
- Every `/rest` request sends `Linkedin-Version: 202607` and
  `X-Restli-Protocol-Version: 2.0.0`. The version is explicit runtime
  configuration so it cannot drift silently.
- Refresh: capability is advertised only when
  `POSTQRON_F05_LINKEDIN_PROGRAMMATIC_REFRESH_APPROVED=true`. LinkedIn limits
  programmatic refresh tokens to approved partners; otherwise expiry requires
  explicit reconnect.
- Revocation: LinkedIn does not document a per-resource token revocation
  endpoint. Disconnect therefore performs guaranteed encrypted local
  credential deletion and does not claim remote revocation.

LinkedIn is `available` only with complete credentials, explicit API version,
review approval, separate runtime audit and smoke approvals, and the shared F5
cipher. Missing inputs fail closed as `not_configured`, `review_required`, or
`audit_required`.
The central provider-neutral scope and cipher bootstrap dependency is supplied
by #321 for #318. This adapter uses that contract without duplicating its
central files.

## Runtime keys

| Variable | Purpose |
| --- | --- |
| `POSTQRON_F05_LINKEDIN_ENABLED` | Exact `true` opt-in. |
| `POSTQRON_F05_LINKEDIN_CLIENT_ID` / `POSTQRON_F05_LINKEDIN_CLIENT_SECRET` | Server-side application credentials. |
| `POSTQRON_F05_LINKEDIN_REDIRECT_URL` | Exact HTTPS callback URL. |
| `POSTQRON_F05_LINKEDIN_API_VERSION` | Required `YYYYMM` version; currently `202607`. |
| `POSTQRON_F05_LINKEDIN_REVIEW_APPROVED` | Exact `true` after the required LinkedIn product/access review. |
| `POSTQRON_F05_LINKEDIN_RUNTIME_AUDIT_VERIFIED` | Exact `true` after the offline fixture and security audit. |
| `POSTQRON_F05_LINKEDIN_SMOKE_VERIFIED` | Exact `true` only after environment smoke verification. |
| `POSTQRON_F05_LINKEDIN_PROGRAMMATIC_REFRESH_APPROVED` | Exact `true` only for a partner explicitly approved for programmatic refresh. |

All values come from the runtime secret store. Fixtures use non-routable
placeholder values and an in-memory HTTP transport; they make no live calls.

## Official sources

- [Posts API](https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/posts-api)
- [Organization access control by role](https://learn.microsoft.com/en-us/linkedin/marketing/community-management/organizations/organization-access-control-by-role)
- [Organization lookup](https://learn.microsoft.com/en-us/linkedin/marketing/community-management/organizations/organization-lookup-api)
- [Increasing access](https://learn.microsoft.com/en-us/linkedin/marketing/increasing-access)
- [Three-legged OAuth](https://learn.microsoft.com/en-us/linkedin/shared/authentication/authorization-code-flow)
- [Programmatic refresh tokens](https://learn.microsoft.com/en-us/linkedin/shared/authentication/programmatic-refresh-tokens)
- [Sign in with LinkedIn using OpenID Connect](https://learn.microsoft.com/en-us/linkedin/consumer/integrations/self-serve/sign-in-with-linkedin-v2)
- [Marketing API versioning](https://learn.microsoft.com/en-us/linkedin/marketing/versioning)
