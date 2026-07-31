# Mastodon provider verification

Verified against the official Mastodon documentation on 2026-07-30:

- [Logging in with an account](https://docs.joinmastodon.org/client/authorized/)
- [OAuth API methods](https://docs.joinmastodon.org/methods/oauth/)
- [OAuth security and PKCE](https://docs.joinmastodon.org/spec/oauth/)
- [Instance API](https://docs.joinmastodon.org/methods/instance/)
- [Application registration](https://docs.joinmastodon.org/methods/apps/)

## Implemented provider boundary

`MastodonDiscovery` accepts only an HTTPS origin. It obtains
`/api/v2/instance` and `/.well-known/oauth-authorization-server`, requires
Mastodon 4.x compatibility, binds OAuth endpoints to the discovered origin,
and enables S256 PKCE for Mastodon 4.3 or newer. OAuth scopes are the
provider-documented granular scopes needed to discover the authenticated
account and later publish statuses/media:

`read:accounts write:media write:statuses`

`MastodonAdapter` implements Authorization Code exchange, optional refresh
only when the instance metadata advertises `refresh_token`, account discovery
through `/api/v1/accounts/verify_credentials`, credential verification and
idempotent `/oauth/revoke`. Missing refresh support returns
`ErrNotRefreshable`; revoke errors are never reported as remote success by the
adapter.

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
- client ID, client secret, and HTTPS redirect URL are configured.

Any missing or mismatched input leaves the provider fail-closed. The runtime
registers Mastodon only through the centralized dynamic registry, preserving
per-attempt instance discovery state and requiring remote revocation before
local deletion.

No client ID, client secret, authorization code or token is sent to browser
bootstrap data or logs.
