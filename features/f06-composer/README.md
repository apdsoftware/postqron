# F06 — Composer runtime

F6 owns provider-agnostic draft authoring, optimistic autosave, revision history,
destination validation, and temporary media ingestion. It does not own social
connections (F5), scheduling (F7), publishing (F8), or the reusable media
library (F19).

## Why the previous F6 was invisible to the UI

The original slice contained domain, HTTP, PostgreSQL, OpenAPI, and browser
validation code, but its manifest had no `server.routes`, it did not implement
the runtime `Module`/`NewPostgresModule` contract, the API had no F6 factory,
and the API image did not resolve the F6 module. Discovery therefore treated
F6 as metadata-only and mounted no UI-callable routes.

This version declares every private route plus a public `OPTIONS` twin, provides
the runtime factory, consumes the authenticated account context established by
the API session middleware, and authorizes active Owner/Member workspace
memberships server-side.

## Capability contract and issue #301

`GET /api/v1/workspaces/{workspace_id}/composer/capabilities` is the only source
of provider/channel/format constraints. The validator supports these
provider-independent families:

- text and links;
- image and carousel;
- video and short/reel;
- thread;
- capability-declared destination fields.

The catalog supplies text, URL, media count/type/size/dimension/ratio/duration,
codec, thread, and destination-field rules. Both Go and TypeScript validators
consume that same shape and return destination, field, rule, code, remedy, and
details.

The provider matrix in #301 is still open. F6 therefore starts with a
`pending-d02-301` blocked catalog and no available provider capabilities. It
does not infer or preserve the previous Meta-only limits. A reviewed catalog
can later be injected through `POSTQRON_F06_CAPABILITIES_JSON`; unavailable
entries require an explicit reason and validation always fails closed.

`test/fixtures/capabilities.json` covers every content family but is expressly a
test fixture, not a declaration that any provider is configured or approved.

## Drafts, autosave, and revisions

Incomplete drafts remain saveable and return their current validation report.
`PUT` and `PATCH` require `expected_revision`; stale writes return `409`.
`autosave_key` is optional, but when supplied it is idempotent per draft:
retries replay the committed revision instead of creating another revision.
The immutable revision list is available at `GET .../revisions`.

Draft content can hold common text/link/media/thread values, multiple
destinations, per-destination overrides, ordered media IDs, and fields declared
by a capability. Provider credentials are never accepted or returned.

## Scheduling boundary for #364

F6 now exposes a narrow runtime boundary for F7 through
`Service.SchedulingBoundary()` and `Module.SchedulingBoundary()`.

- `ValidateForScheduling(SchedulingValidationCommand)` authorizes the actor,
  rechecks live channel capability resolution and live media metadata through
  F6-owned boundaries, requires the exact normalized set of requested
  `channel_ids`, and returns an immutable `SchedulingDraftReference` with
  `draft_id`, `draft_revision`, normalized `channel_ids`, and
  `capability_version`.
- The boundary fails closed on missing live dependencies with typed
  `ErrDependencyUnavailable`; invalid content still returns typed
  `ErrValidation` via `ValidationFailure`.
- Capability drift is revision-visible: if the live resolved channel type or
  capability differs from the stored draft revision, validation fails instead
  of silently rewriting the snapshot.

`DuplicateDraft(DuplicateDraftCommand)` clones one exact stored revision into a
new independent draft. The clone copies each referenced media object into a new
F6-owned object key and metadata row before draft creation, so deleting the
source draft does not invalidate the clone. The operation is compensable rather
than externally idempotent in this issue scope: if a downstream consumer
persists no durable state after `DuplicateDraft` succeeds, it must compensate by
deleting the returned clone at revision `1`. On internal failures before the new
draft commits, F6 cleans up cloned media rows and objects.

## Temporary media ingestion

Media upload authorization is bound to the authenticated account and workspace:

1. `POST .../composer/media` declares name, type, and byte length and returns
   a short-lived signed object-store upload URL, its required headers, and an
   authenticated completion URL.
2. The browser sends bytes directly to the S3-compatible store. The API process
   never accepts or buffers the object body.
3. `POST .../complete` verifies the stored size and declared content type, then
   streams bounded image/MP4 inspection before changing the record to `ready`.
4. Draft saves replace client-supplied metadata with the canonical inspected
   record before validation.
5. `GET .../download` returns a short-lived signed download URL. Unattached
   objects can be deleted; attachment changes their lifecycle tag to retained.

PostgreSQL stores only metadata, inspection state, expiry, and the private
object key. Object bytes remain in S3-compatible storage. Configure that bucket
to expire objects tagged `postqron-lifecycle=temporary`; retained objects are
retagged `postqron-lifecycle=retained` and must be excluded from that expiry
rule. The bucket CORS policy must allow the product's exact browser origins and
the signed `PUT` headers returned by the API.

Draft/media changes are consistency-safe across the external object boundary:
new objects are retained before mutation, while the draft, immutable revision,
and media attachment metadata commit in one PostgreSQL transaction. A failed
transaction compensates retained objects back to temporary. Removed objects
receive a fresh safe expiry in that same transaction, then their temporary tag
is synchronized. Failed tag synchronization is recorded and retried by later
media/draft operations; until retry succeeds the object remains safely retained
and no committed draft references it.

Storage is fail-closed. With no S3 configuration, draft operations remain
available but media operations return client-safe `503` errors with
`retryable: true`. A partial or invalid storage configuration prevents F6 from
starting.

Runtime storage configuration:

- `POSTQRON_F06_S3_ENDPOINT`: absolute S3-compatible endpoint.
- `POSTQRON_F06_S3_REGION`: signing region.
- `POSTQRON_F06_S3_BUCKET`: private object bucket.
- `POSTQRON_F06_S3_ACCESS_KEY_ID` and
  `POSTQRON_F06_S3_SECRET_ACCESS_KEY`: runtime credentials; never expose these
  values in client configuration, logs, fixtures, or source control.
- `POSTQRON_F06_S3_PATH_STYLE`: optional boolean, defaults to `true`.
- `POSTQRON_F06_S3_ALLOW_INSECURE_ENDPOINT`: optional boolean, defaults to
  `false`. Browser-facing signed URLs require HTTPS by default. Plain HTTP is
  accepted only for a loopback endpoint or when this development-only override
  is explicitly `true`.
- `POSTQRON_F06_MAX_UPLOAD_BYTES`: optional positive operational ceiling. It is
  not a provider capability or provider-specific media limit.

This boundary is intentionally provider-agnostic. It does not make media
searchable/reusable across drafts and does not implement F19.

## CORS and security

All browser routes use exact origins from `POSTQRON_AUTH_ALLOWED_ORIGINS`,
credentialed CORS, explicit preflight methods/headers, `Vary: Origin`, and
fail-closed hostile/multiple origins. Private routes remain behind the shared
product-session middleware; the F6 handler reads only the authenticated runtime
context and checks active workspace membership.

## Verification

```sh
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
pnpm exec tsc --noEmit -p features/f06-composer/tsconfig.json
node --experimental-strip-types --test \
  features/f06-composer/test/*.test.ts
POSTQRON_FEATURE_ROOTS="services/api/features:features" \
  pnpm migrations:check
```

PostgreSQL integration:

```sh
F06_DATABASE_URL="postgres://..." GOWORK=off \
  go test -race -run 'TestPostgres.*Integration' ./...
```

The wire contract is `contracts/composer.openapi.yaml`.
