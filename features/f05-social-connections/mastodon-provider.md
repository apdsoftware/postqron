# Mastodon provider verification

Verified against the official Mastodon documentation on 2026-07-30:

- [Logging in with an account](https://docs.joinmastodon.org/client/authorized/)
- [OAuth API methods](https://docs.joinmastodon.org/methods/oauth/)
- [OAuth security and PKCE](https://docs.joinmastodon.org/spec/oauth/)
- [Instance API](https://docs.joinmastodon.org/methods/instance/)
- [Application registration](https://docs.joinmastodon.org/methods/apps/)

## Implemented provider boundary

`MastodonDiscovery` accepts only an HTTPS origin. It obtains
`/api/v2/instance`, requires Mastodon 4.x compatibility, canonicalizes the
official metadata issuer with its trailing slash, and binds every OAuth
endpoint to the discovered origin. Mastodon 4.3 or newer must publish valid
`/.well-known/oauth-authorization-server` metadata; Mastodon 4.0-4.2 falls
back only to the same-origin official endpoints (`/oauth/authorize`,
`/oauth/token`, `/oauth/revoke`, `/api/v1/apps`). S256 PKCE is enabled only on
4.3+. OAuth scopes are the provider-documented granular scopes needed to
discover the authenticated account and later publish statuses/media:

`read:accounts write:media write:statuses`

`MastodonAdapter` implements Authorization Code exchange, optional refresh
only when metadata advertises `refresh_token`, account discovery through
`/api/v1/accounts/verify_credentials`, credential verification and idempotent
`/oauth/revoke`. The runtime dynamic adapter registers a dedicated OAuth app
per origin through `app_registration_endpoint` or the same-origin fallback
`POST /api/v1/apps`, binds the returned client ID/secret back to that origin,
and persists them only inside the centrally encrypted F5 attempt/session
`ProviderState`. Missing refresh support returns `ErrNotRefreshable`; revoke
errors are never reported as remote success by the adapter.

The account URL, not the instance-local numeric account ID, is used as the
remote identifier to avoid collisions between independent instances.

## Network security

All dynamic requests use the provider-specific hardened transport:

- HTTPS only, with no userinfo or fragments;
- fresh DNS/IP validation for every request;
- DNS results pinned into the dial, preventing validation-to-connect rebinding;
- private, loopback, link-local, carrier-grade NAT, documentation, benchmark,
  multicast and reserved IPv4/IPv6 ranges blocked;
- no redirects followed, including redirects to another origin or a private
  target;
- proxy inheritance disabled;
- request timeout and 1 MiB response limit;
- exact JSON/status validation and client-safe 401/403/404/429/5xx
  classification.

Fixtures use local TLS servers behind an injected resolver/dialer. They make no
live provider calls and cover multiple origins/versions, discovery, exchange,
PKCE, profile, refresh, revoke, malformed/oversized responses, redirects,
private IPs, DNS pinning and provider error classes.

## Runtime gate

Production wiring now uses the typed dynamic-provider hook introduced by
`feat/351-dynamic-adapter-wiring`. Mastodon becomes available only when all of
the following are true:

- `enabled=true`;
- runtime audit and smoke attestations are both `true`;
- `compatibility_version` exactly matches
  `f05_dynamic_runtime_v1`;
- an HTTPS redirect URL is configured.

Any missing or mismatched input leaves the provider fail-closed. The runtime
registers Mastodon only through the centralized dynamic registry, preserving
per-attempt instance discovery state, per-origin registered app credentials,
and remote-revocation requirements before local deletion.

No client ID, client secret, authorization code or token is sent to browser
bootstrap data or logs.
