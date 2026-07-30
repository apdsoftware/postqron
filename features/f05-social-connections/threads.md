# Threads F5 adapter verification

Verified on 2026-07-30 against Meta's official Threads documentation and the
official Meta Threads Postman collection.

## Mounted lifecycle

- Authorization Code flow at
  `https://www.threads.com/oauth/authorize`. The former `.threads.net` endpoint
  currently redirects to `.threads.com`.
- Server-side code exchange at
  `POST https://graph.threads.net/oauth/access_token`.
- Server-side exchange of the short-lived token for a long-lived token at
  `GET https://graph.threads.net/access_token`.
- Explicit discovery of the authorized profile at
  `GET https://graph.threads.net/me`.
- Verification by the selected Threads user ID.
- Verification of app ownership, validity, user identity, and granted scopes
  through the official access-token debugger using a server-side app token.
- Refresh of an unexpired long-lived token at
  `GET https://graph.threads.net/refresh_access_token`.
- Minimum publishing scopes: `threads_basic` and
  `threads_content_publish`.

The shared F5 service supplies a one-time `state`, encrypts credentials before
selection storage, requires explicit profile selection, and marks
authentication/permission/resource failures for reconnection.

## Deliberate fail-closed limits

- PKCE is disabled because Meta's documented Threads exchange does not define
  `code_challenge` or `code_verifier`.
- Remote grant revocation is not advertised because the official Threads
  lifecycle does not document a revocation endpoint. F5 always deletes the
  local encrypted credential and reports that provider-side revocation was not
  confirmed.
- Runtime availability requires an exact `true` feature flag, complete
  credentials, App Review approval, and a separate runtime audit/smoke flag.
- Endpoint overrides are test-only. Fixtures use placeholder values and never
  call Meta.

## Provider query-string limitation

Meta's official collection currently requires the app secret in query
parameters for the short-lived `POST /oauth/access_token`, long-lived
`GET /access_token`, and app-token `GET /oauth/access_token` requests. It does
not document `application/x-www-form-urlencoded` body parameters for the
Threads short-lived exchange, so this adapter does not assume that unsupported
transport.

The adapter mitigates the provider constraint by keeping every token request
server-side, never returning or wrapping URL-bearing transport errors, never
following redirects from Threads API endpoints, and exposing only stable error
codes. Application logging must not log outbound request URLs.

## Official sources

- <https://developers.facebook.com/docs/threads/get-started/>
- <https://developers.facebook.com/docs/threads/threads-api-publishing/>
- <https://www.postman.com/meta/threads/documentation/dht3nzz/threads-api>
