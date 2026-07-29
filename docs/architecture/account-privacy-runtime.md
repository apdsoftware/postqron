# Account privacy runtime

The F12 runtime persists export work in PostgreSQL and claims jobs with
`FOR UPDATE SKIP LOCKED`. A processing lease makes abandoned work reclaimable;
request IDs are the queue idempotency keys. Retries store bounded error codes,
never exception payloads, account email addresses, tokens, or artifact content.

Exports are ZIP files containing only authoritative account/profile,
membership, consent, draft, and scheduling projections. OAuth attempts,
sessions, password material, provider tokens, internal diagnostics, and
credential ciphertext are deliberately excluded. Before any bytes reach disk,
the ZIP stream is split into 64 KiB chunks and encrypted with AES-256-GCM using
independent random nonces and counter-bound additional authenticated data.
Files are created with mode `0600` below `POSTQRON_PRIVACY_ARTIFACT_DIR`, which
must be a private volume shared only by the API and worker. The API authenticates
the complete artifact in a no-output pass, rewinds it, and then decrypts chunks
while streaming the download. A wrong key, modified chunk, truncation, or
trailing data therefore produces no plaintext response. Exports expire after
seven days.

The download endpoint stores only a SHA-256 token digest. Tokens are random,
short-lived, bound to the account, export, and exact object key, and consumed
atomically before serving a regular file. Object keys cannot be absolute,
normalized to another value, or escape the private root.

## F3 account-access boundary

The runtime depends on this narrow adapter and no F3 persistence details:

```go
type AccountAccessBoundary interface {
    Freeze(context.Context, string) error
    Restore(context.Context, string) error
    Finalize(context.Context, string) error
}
```

API and worker construct the public #228 implementation with
`auth.NewAccountAccessBoundary(auth.NewPostgresStore(db), clock)`. The runtime
keeps only the three-method interface above and never reaches into F3 storage
or invokes dynamically discovered SQL.

Freeze must serialize against login/link/callback, reject new sessions, revoke
existing sessions and pending OAuth attempts, and be idempotent. Restore may
reactivate access during grace but must not recreate sessions or deleted social
tokens/jobs. Finalize must remove F3 identifying data idempotently.

Provider revocation is a separate mandatory boundary. Identity-provider
revocation uses the configured public F3 provider adapters before local
ciphertext is removed. If a target workspace still has a connected social
credential and no public F5 revocation boundary is mounted, deactivation fails
closed and never reports `ProviderRevocationAttempted`. If an account was
already frozen, any later deactivation failure invokes F3 `Restore` as a
compensating action; invalidated sessions and tokens remain invalidated.

## Operations

Configure both API and worker with the same artifact directory and configure the
API with its externally reachable HTTPS base URL:

- `POSTQRON_PRIVACY_ARTIFACT_DIR=/var/lib/postqron/privacy-exports`
- `POSTQRON_PRIVACY_DOWNLOAD_BASE_URL=https://api.example.com`
- `POSTQRON_PRIVACY_ARTIFACT_KEY_B64=<base64 of exactly 32 random bytes>`
- `POSTQRON_PRIVACY_ALLOWED_ORIGINS=https://app.example.com`

`POSTQRON_ENV=production` makes the artifact key mandatory and permits only an
absolute HTTPS download origin. Outside production, HTTP is accepted only for
`localhost` or an IP loopback address. Rotate the artifact key only after all
exports encrypted with the prior key have expired or been purged.
`POSTQRON_PRIVACY_ALLOWED_ORIGINS` is a comma-separated exact APP-origin
allowlist for both the authenticated capability issuer and the public cancel
submission. It is mandatory in production and entries must use HTTPS; no API
origin is inferred. In development and tests only, an omitted value falls back
to the loopback download base origin. Missing request origins, malformed
origins, and unlisted origins fail before authentication, cookie processing, or
database side effects.

## Cancellation after account freeze

F3 freeze revokes the requesting session and prevents login, so the original
authenticated `DELETE /account/deletions/{id}` cannot cancel an account
deletion. Before requesting deletion, the client now obtains a random
pre-authorized capability from
`POST /account/deletion-cancel-capabilities`. Only its SHA-256 digest is stored.
The raw value is delivered only as an `HttpOnly`, `SameSite=Strict` cookie whose
path is restricted to `/api/v1/account/deletions/`; it is `Secure` in
production. The JSON response contains only `expires_at`, so Vue state,
localStorage, logs, and the public-page URL never receive the secret. The public
page carries only the deletion id.

During grace, the frozen user's browser submits the cookie once to the public
`POST /account/deletions/{id}/cancel` endpoint. The runtime binds it to the
deletion account and grace state using one atomic SQL claim, so concurrent or
replayed requests cannot enter F12 twice. A retryable F12 failure releases the
claim; success consumes it and clears the cookie. The normal F12
`CancelDeletion` transition restores F3 access without
recreating sessions, jobs, or provider tokens. The launch-readiness E2E proves
the old session receives 401 and exactly one of two concurrent submissions
succeeds.

Alert on `privacy_runtime_failed`, repeated `finalization_failed` requests, jobs
at the maximum attempt count, artifacts past retention, or a growing claim
backlog. Logs intentionally contain event names, safe codes, and counts only.
Never attach the private volume or its files to CI artifacts.
