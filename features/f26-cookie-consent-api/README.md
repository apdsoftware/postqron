# F26 — Cookie consent ledger and API

This slice owns the server-side source of truth behind
`/api/v1/cookie-preferences`.

## Privacy and consent rules

- `necessary` is always true and is deliberately absent from write payloads.
- `preferences`, `analytics`, and `marketing` are separate opt-ins and default
  to false.
- A choice is valid for at most six calendar months and only for the exact
  current Cookie Policy version and SHA-256 digest.
- The server resolves the approved current policy from
  `compliance_legal_documents`; a client-supplied version is only a
  precondition and cannot select or invent a policy.
- Anonymous visitors receive a random HttpOnly host cookie. Authenticated
  sessions resolve to their account through the existing hashed session
  ledger. IP addresses and user agents are not stored.
- Every write appends one event per optional category. Turning off a
  previously enabled category records `withdrawn`; a first refusal records
  `rejected`.
- Idempotency is scoped to the resolved subject. The same key and input replay
  the original response; the same key with different input returns `409`.
- Subject mappings are separate from immutable evidence. Erasure deletes the
  mapping, current preference, and retry records, leaving retained evidence
  linked only to an unrecoverable random internal key.
- Evidence expires after 12 months. `PurgeExpiredEvidence` is the worker hook
  for retention enforcement.

The module remains fail-closed until F25 has supplied an approved, published,
effective `cookies_it` row. No placeholder version is compiled into this
slice.

## API

```text
GET    /api/v1/cookie-preferences
PUT    /api/v1/cookie-preferences
DELETE /api/v1/cookie-preferences
GET    /api/v1/cookie-preferences/export
```

`PUT` requires `Content-Type: application/json`, a same-origin request, an
`Idempotency-Key`, the current `policy_version`, an allowlisted `source`, and
all three optional booleans. Unknown fields are rejected.

## Runtime boundary

`NewPostgresModule` implements the feature-host lifecycle and named-handler
contract. It requires a `*sql.DB` and clock from the host. The manifest owns
the public route declarations and migration; no central route table is needed.

## Verification

```sh
(cd features/f26-cookie-consent-api && GOWORK=off go test ./...)
(cd features/f26-cookie-consent-api && \
  F26_DATABASE_URL=postgres://... GOWORK=off go test ./... \
    -run TestPostgresRepositoryIntegration)
pnpm migrations:check
pnpm test:runtime-bundle
```
