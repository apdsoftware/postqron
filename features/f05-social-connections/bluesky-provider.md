# Bluesky / AT Protocol provider verification

Verified against the current official documentation on 2026-07-30:

- [AT Protocol OAuth specification](https://atproto.com/specs/oauth)
- [OAuth client implementation](https://docs.bsky.app/docs/advanced-guides/oauth-client)
- [OAuth implementation patterns](https://atproto.com/guides/oauth-patterns)
- [OAuth permission sets](https://atproto.com/guides/permission-sets)
- [Handle specification](https://atproto.com/specs/handle)
- [DID specification](https://atproto.com/specs/did)
- [Creating a post](https://docs.bsky.app/docs/tutorials/creating-a-post)

Password and app-password authentication are intentionally absent.

## Implemented provider boundary

`BlueskyOAuthClient` starts from an HTTPS PDS origin and:

1. fetches `/.well-known/oauth-protected-resource`;
2. fetches and strictly validates the selected authorization server metadata;
3. requires authorization code and refresh grants, S256 PKCE, PAR,
   `authorization_response_iss_parameter_supported`, client-ID metadata,
   ES256 DPoP, and public/confidential token-auth metadata;
4. creates a new P-256 DPoP key per attempt;
5. performs PAR with one-time state, PKCE and DPoP nonce retry;
6. stores state, PKCE verifier, issuer/PDS binding, DPoP key and nonce only in
   an AEAD-encrypted one-time attempt;
7. validates callback `state` and `iss`, exchanges the code with DPoP, and
   requires `sub`, `scope`, `refresh_token`, token type `DPoP`, and a response
   nonce;
8. resolves `did:plc` or `did:web`, discovers `#atproto_pds`, and revalidates
   the PDS-to-issuer relationship;
9. obtains the profile using `Authorization: DPoP`, a fresh ES256 proof with
   `ath`, and an independently tracked Resource Server nonce;
10. rotates access token, single-use refresh token, and Authorization Server
    nonce during refresh.

Completed sessions can only be persisted through `SealSession`, which encrypts
access/refresh tokens, AS/RS nonces and the DPoP private key with
session-specific AAD. Exported session fields are excluded from JSON.

The requested least-privilege connection/publishing scope set is:

`atproto repo:app.bsky.feed.post?action=create blob:*/* rpc:app.bsky.actor.getProfile?aud=did:web:api.bsky.app#bsky_appview`

DPoP proofs have unique `jti` values and never include the private key. A
missing `DPoP-Nonce` on any DPoP response is rejected. A `use_dpop_nonce`
response is retried once with a new proof and the rotated nonce.

AT Protocol authorization-server metadata does not currently define a token
revocation endpoint. `Revoke` therefore returns an explicit error and never
claims remote revocation.

## Network and fixture security

Protected-resource metadata, authorization metadata, PAR/token endpoints,
`did:web`, PLC documents and PDS service endpoints all pass through the same
HTTPS-only, DNS-pinned SSRF transport described in
`mastodon-provider.md`. Redirects are rejected and response bodies are bounded.

Offline TLS fixtures cover multiple PDS origins, protected-resource and
authorization metadata, PAR, PKCE, DPoP proof uniqueness, AS/RS nonce rotation,
callback replay, DID/PDS binding, profile, refresh-token rotation, remote
revoke failure, redirects, 401/403/404/429/5xx, malformed payloads, private
addresses and DNS rebinding. Fixture values are synthetic and no live calls
are made.

## Runtime gate

Bluesky remains `unavailable` even when explicit compatibility, audit and
smoke attestations are present. The current central boundary returns a bare
access-token string and cannot persist encrypted DPoP/session state or sign
each F8 request. Activation is blocked by
[issue #324](https://github.com/apdsoftware/postqron/issues/324); weakening
PAR, PKCE, DPoP, nonce handling or falling back to app passwords is prohibited.
