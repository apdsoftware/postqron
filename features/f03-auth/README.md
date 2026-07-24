# F3 authentication and onboarding

This autonomous API slice implements federated login, registration, explicit
account linking, versioned legal receipts, sessions, provider unlinking, and
the onboarding hand-off to F4.

## Security boundaries

- Every authorization request uses a random single-use `state`, an OIDC
  `nonce`, and PKCE with `S256`. Only the state digest is stored; the verifier
  and nonce are encrypted while the short-lived attempt is pending.
- Google, Apple, Facebook, and LinkedIn adapters are all required at startup.
  Provider client IDs, client secrets/assertions, signing keys, and the
  32-byte auth data-encryption key must come from the runtime secret store.
- A provider adapter must exchange the code with the supplied PKCE verifier,
  validate the ID token signature, issuer, audience, expiry, and expected
  nonce where OIDC is used, and return the provider's stable subject. It must
  never trust browser-supplied profile claims.
- A verified email collision never auto-links. Linking starts only from a
  recently authenticated session, binds the target account and exact session
  to the OAuth attempt, and rejects identities already owned by another
  account.
- Account, provider identity, legal evidence, session, completed attempt, and
  F4 outbox event are committed in one serializable transaction. Retryable
  provider or database errors do not leave a partial account or session.
- Session and OAuth tokens are opaque. Only session digests and encrypted
  provider revocation tokens are persisted. The HTTP boundary uses a
  `Secure`, `HttpOnly`, `SameSite=Lax`, `__Host-` cookie.

`MemoryStore` is the deterministic reference used by unit tests.
`PostgresStore` is the production adapter over the schema in
`migrations/000001_create_auth.sql`; the host injects its existing
`database/sql` connection and PostgreSQL driver.

## Provider configuration

Each adapter supplies its client ID, HTTPS authorization and callback URLs,
scopes, and provider-specific parameters. Minimum scopes are enforced:

| Provider | Minimum sign-in scopes | Notes |
| --- | --- | --- |
| Google | `openid email` | Discover endpoints and signing keys from Google's OIDC metadata. |
| Apple | `email` | Use `form_post` when configured and a short-lived client-secret assertion from the secret store. |
| Facebook | `email` | Pin the approved Graph API version in deployment configuration. |
| LinkedIn | `openid email` | Use the current OIDC product, not the deprecated legacy sign-in scopes. |

Authoritative setup references:

- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [Sign in with Apple](https://developer.apple.com/documentation/signinwithapplerestapi/request-an-authorization-to-the-sign-in-with-apple-server.)
- [Facebook Login](https://developers.facebook.com/docs/facebook-login/)
- [Sign In with LinkedIn using OpenID Connect](https://learn.microsoft.com/en-us/linkedin/consumer/integrations/self-serve/sign-in-with-linkedin-v2)

## Legal receipts and F4 contract

A new Italian account requires an immutable `terms_it` receipt with action
`accepted` and a `privacy_it` receipt with action `acknowledged`. Each receipt
records document version and SHA-256 digest, purpose, locale, surface,
control-text version, correlation ID, and timestamp. Historical rows are
append-only.

The same transaction emits
`auth.account.onboarding-required` version 1. F4 consumes its
`idempotency_key` to create exactly one personal workspace and an `owner`
membership. The JSON Schema is under `contracts/events/v1`.

## Verification

From this directory:

```sh
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

From the repository root, validate discovery and the forward-only migration:

```sh
POSTQRON_FEATURE_ROOTS=features/f03-auth go run ./services/api/cmd/migrate --check
```
