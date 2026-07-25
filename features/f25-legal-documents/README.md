# F25 — Legal documents publication gate

This slice prepares the publication boundary for Postqron's Terms, Privacy
Policy, and Cookie Policy without claiming that legal review has happened.
The repository intentionally contains no legal copy, approval identifiers, or
provider-review conclusions.

## Current state

Publication is fail-closed. `src/bundle.ts` contains an empty release input, so:

- `/legal/terms`, `/legal/privacy`, and `/legal/cookies` return HTTP 503;
- the versioned API adapter returns `legal_release_blocked` with
  `Cache-Control: no-store`;
- no draft, incomplete translation, or unapproved artifact can be returned;
- this feature must not be marked ready for merge or production release.

The page routes are declared in `feature.yaml` so the runtime can compose them
without editing a central route registry. The API-neutral handler in
`src/api.ts` defines the behavior expected below `/api/v1/legal-documents`.
Connecting that handler to the API host requires a separately reviewed runtime
integration once the release bundle exists; this draft does not weaken the
current API host or modify files outside F25.

## Missing legal artifacts

Counsel or the authorized legal owner must provide all of the following:

1. exact UTF-8 content for `terms`, `privacy`, and `cookies` in `en`, `it`,
   `es`, `fr`, and `de`;
2. the approved contracting entity/controller name and public contact for
   every artifact;
3. an independent approval reference and UTC approval timestamp for every
   document, locale, and version;
4. monotonic `major.minor` versions, publication/effective timestamps, change
   classification, and a revision summary;
5. the real cookie/technology inventory, including category, purpose,
   duration, first/third-party status, recipients, and location;
6. approved retention periods and the B2C/B2B, Paddle, cancellation, renewal,
   withdrawal, liability, and governing-law positions;
7. the verified Postqron DPA reference;
8. a verified Mailronix review covering its DPA, legal entity, role, data
   categories, purposes, retention, processing locations, subprocessors, and
   transfer safeguards;
9. a release-level legal approval reference authorizing the exact artifact
   digests for the Italian launch market.

Public Mailronix marketing statements are not sufficient evidence. The
provider evidence must point to the reviewed contract/DPA and actual
subprocessor record.

## Release controls

`auditLegalRelease` rejects the entire bundle unless:

- all 15 current document/locale combinations are referenced by each release;
- every referenced artifact exists and its SHA-256 digest matches its exact
  content bytes;
- each artifact has complete immutable version, controller, contact,
  approval, publication, effective-date, and change-history metadata;
- no drafting markers are present in content or metadata;
- all required legal/provider evidence records are present and verified;
- the release uses market `IT`, English fallback, and a complete independent
  legal release approval;
- release versions and effective dates increase monotonically.

Previously effective releases remain addressable by version after a newer
release becomes current. Unsupported or unavailable locales explicitly fall
back to English and report that fallback in the response.

## Intended API

The framework-neutral adapter supports:

```text
GET /api/v1/legal-documents/{terms|privacy|cookies}/current?locale=it
GET /api/v1/legal-documents/{terms|privacy|cookies}/versions/{major.minor}?locale=it
```

The legacy keys `terms_it`, `privacy_it`, and `cookies_it` are accepted at the
adapter boundary for compatibility with the current marketing-site proxy.
Only GET is allowed. Approved responses include the exact digest, version,
locale, approval metadata, permanent URL, and content. Blocked responses never
include legal content.

## Verification

From the repository root:

```sh
node --experimental-strip-types --test features/f25-legal-documents/test/*.test.ts
pnpm exec tsc --noEmit -p features/f25-legal-documents/tsconfig.json
pnpm exec eslint features/f25-legal-documents
pnpm test:runtime-bundle
pnpm --filter @postqron/web build
```

The tests use synthetic fixtures that are never loaded by the production
bundle and do not represent legal approval.
