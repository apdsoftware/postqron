# F13 — Compliance and legal documents

This autonomous slice implements the technical controls from D04 without
claiming legal approval:

- immutable `major.minor` legal artifacts, exact SHA-256 digests, permanent
  version URLs, effective dates, and supersession;
- a hard distinction between `placeholder` and `approved` content;
- append-only, idempotent evidence for acceptance, acknowledgement, consent,
  refusal, and withdrawal;
- cookie categories `necessary`, `preferences`, `analytics`, and `marketing`;
- privacy-safe defaults, a six-month maximum choice lifetime, first-level
  accept/reject/customise actions with equal prominence, granular revisions,
  and immediate tracker revocation;
- API contracts and a forward-only PostgreSQL migration owned by the slice.

Only `necessary` is enabled before a valid choice. Optional trackers must be
registered with `CookieConsentManager`; their `activate` callback is never
called before the corresponding opt-in, while `revoke` is called immediately
when a granted category is withdrawn.

## Legal publication gate

Drafting aids live in `content/placeholders`. They cannot carry approval or
publication metadata and are rejected by both the domain model and database
constraints.

After counsel approves exact copy, add the immutable artifact under
`content/approved/<document_key>/<major.minor>.md`, calculate the digest from
the exact rendered bytes, and record the external legal approval identifier.
Do not overwrite an existing artifact for corrections; publish a higher
version.

## Verification

From the repository root:

```sh
pnpm exec tsc --noEmit -p features/f13-compliance/tsconfig.json
pnpm exec eslint features/f13-compliance
node --experimental-strip-types --test features/f13-compliance/test/*.test.ts
POSTQRON_FEATURE_ROOTS="services/api/features:features/f13-compliance" pnpm migrations:check
```

The slice is discovered recursively through `feature.yaml`; it does not
require a central registry change.
