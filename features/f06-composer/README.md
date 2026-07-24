# F06 — Composer and media validation

This autonomous API slice owns draft text/media, multi-destination selection,
and the validation gate used before scheduling. It implements the launch
contract from D02 for Facebook Pages and Instagram Professional accounts.

## Behavior

- incomplete drafts remain saveable and always return their current validation
  report;
- create, read, list, update, and delete operations are scoped to a workspace
  and require the caller to pass the `ContentAuthorizer` boundary;
- updates and deletes use a positive expected revision, so two composer
  sessions cannot silently overwrite one another;
- destination text and ordered media can inherit draft defaults or use explicit
  overrides;
- every selected destination receives an independent `valid` result and a list
  of stable `field`, `rule`, and `code` errors;
- `ValidateForScheduling` and `POST .../validate` reject the whole operation
  when any destination is invalid;
- server text is normalized to NFC and both Go and browser validators count
  Unicode code points after normalization;
- media validation is metadata-only. Upload authorization, object inspection,
  and storage lifecycle belong at the media-ingestion boundary; callers must
  populate the inspected codec, dimensions, duration, and container fields.

The browser validator in `client/validation.ts` provides early feedback, but
the Go validator is authoritative and repeats all rules server-side. Neither
the draft payload nor the database contains social-provider credentials.

## HTTP endpoints

All endpoints live below `/api/v1/workspaces/{workspace_id}`:

| Method | Path | Result |
| --- | --- | --- |
| `POST` | `/drafts` | Create a draft and return validation |
| `GET` | `/drafts` | List workspace drafts and validation |
| `GET` | `/drafts/{draft_id}` | Read one draft and validation |
| `PUT` | `/drafts/{draft_id}` | Replace content using `expected_revision` |
| `DELETE` | `/drafts/{draft_id}?revision=N` | Delete the expected revision |
| `POST` | `/drafts/{draft_id}/validate` | Scheduling validation gate |

The full wire contract is in `contracts/composer.openapi.yaml`.

## Verification

Run the slice independently because issue #16 may only modify this directory
and therefore cannot add the module or package to root workspace registries:

```sh
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
pnpm exec tsc --noEmit -p features/f06-composer/tsconfig.json
node --experimental-strip-types --test \
  features/f06-composer/test/*.test.ts
POSTQRON_FEATURE_ROOTS="services/api/features:features" \
  pnpm migrations:check
```

With the migration already applied to a disposable PostgreSQL database:

```sh
F06_DATABASE_URL="postgres://..." GOWORK=off \
  go test -race -run TestPostgresRepositoryIntegration ./...
```
