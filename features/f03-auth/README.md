# F3 authentication and onboarding

This autonomous API slice implements self-service password registration with
email verification, federated login, explicit account linking, versioned legal
receipts, sessions, provider unlinking, and the onboarding hand-off to F4.

## Security boundaries

- Every authorization request uses a random single-use `state`, an OIDC
  `nonce`, and PKCE with `S256`. Only the state digest is stored; the verifier
  and nonce are encrypted while the short-lived attempt is pending.
- Google, Apple, Facebook, and LinkedIn adapters are configured independently
  at runtime. A missing or invalid provider configuration disables only that
  provider; it must never block password registration/login or the other valid
  providers. Client IDs, client secrets/assertions, and the 32-byte auth
  data-encryption key come from the runtime secret store and never from the
  repository.
- A provider adapter must exchange the code with the supplied PKCE verifier,
  validate the ID token signature, issuer, audience, expiry, and expected
  nonce where OIDC is used, and return the provider's stable subject. It must
  never trust browser-supplied profile claims.
- `POST /api/v1/auth/password/register` creates the account, Argon2id
  credential, immutable legal receipts, one-time email-verification token
  digest, security event, and `auth.account.onboarding-required` outbox event
  in one serializable transaction. The raw verification token never appears in
  persisted state, audit rows, or API payloads.
- `POST /api/v1/auth/password/verify` consumes the one-time token, marks the
  email verified, and invalidates sibling verification tokens for the same
  account. `POST /api/v1/auth/password/verify/resend` rotates the pending
  token only after the resend interval has elapsed.
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
- Password login uses Argon2id with the checked-in security floor
  (`m=65536,t=3,p=1`, 16-byte salt, 32-byte key), generic credential errors,
  progressive account lockout, and immutable success/failure security events.
- `POST /api/v1/auth/logout` validates the CSRF token bound to the opaque
  session, revokes that session server-side, appends `session.logged_out`
  without request secrets, and only then expires the cookie. An already absent
  session is safe to log out again.
- `POST /api/v1/auth/password/change` requires the current password, matching
  confirmation, the same 12–1024-byte policy used at credential creation,
  a session-derived CSRF token, and password authentication no older than five
  minutes. Rejected current-password attempts have a separate progressive rate
  limit and append only a secret-free `password.change_failed` event.
- A successful password change runs in one serializable transaction: it locks
  and compares the credential and current session, updates the Argon2id hash,
  revokes all account sessions, inserts one new session, and appends
  `password.changed`. Optimistic hash/session checks make concurrent requests
  fail safely; retrying an already committed request cannot create a second
  active replacement session. Passwords, hashes, and raw session tokens never
  appear in API bodies, audit rows, or logs.

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
| Facebook | `email` | Use the Meta Graph token exchange plus token inspection and pin the approved Graph API version in deployment configuration. |
| LinkedIn | `openid email` | Use the current OIDC product, not the deprecated legacy sign-in scopes. |

Runtime environment variables recognized by this slice:

- `POSTQRON_AUTH_ENCRYPTION_KEY_B64`
- `POSTQRON_AUTH_GOOGLE_CLIENT_ID`, `POSTQRON_AUTH_GOOGLE_CLIENT_SECRET`, `POSTQRON_AUTH_GOOGLE_REDIRECT_URL`
- `POSTQRON_AUTH_APPLE_CLIENT_ID`, `POSTQRON_AUTH_APPLE_CLIENT_SECRET`, `POSTQRON_AUTH_APPLE_REDIRECT_URL`
- `POSTQRON_AUTH_FACEBOOK_CLIENT_ID`, `POSTQRON_AUTH_FACEBOOK_CLIENT_SECRET`, `POSTQRON_AUTH_FACEBOOK_REDIRECT_URL`, `POSTQRON_AUTH_FACEBOOK_GRAPH_VERSION`
- `POSTQRON_AUTH_LINKEDIN_CLIENT_ID`, `POSTQRON_AUTH_LINKEDIN_CLIENT_SECRET`, `POSTQRON_AUTH_LINKEDIN_REDIRECT_URL`

Cross-slice follow-up intentionally left out of this diff:

- `features/f30-app-shell` bootstrap exposure of configured providers is tracked outside this slice in `#219`.
- Binary/host-level mounting and rollout tasks are tracked outside this slice in `#220`.

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

The complete password registration, verification, login, logout, CSRF,
session-rotation, OAuth callback, and stable error contract is documented in
`contracts/openapi.yaml`.
