# F19 — Media library

This autonomous API slice owns reusable media uploads, trusted metadata,
workspace search, F6 composer references, F10 quota commands, and conservative
object lifecycle.

## Behavior

- upload creation reserves `media_storage_bytes` through the server-side F10
  adapter before any signed object-store request is returned;
- the upload idempotency key is scoped to the workspace and the F10 command
  uses the same stable key, so client retries cannot consume quota twice;
- completion trusts only the configured object inspector and requires the
  inspected content type and byte size to match the reservation;
- metadata includes searchable original name, alt text, normalized tags,
  dimensions, codecs, duration, checksum, and the fields required by F6;
- search is workspace-scoped and returns only ready assets;
- `composer-reference` emits the exact metadata shape accepted by F6 and
  rejects archived assets for new reuse;
- archive is a soft lifecycle action: it hides the asset and blocks new reuse,
  while `ResolveExistingDraft` still resolves it for saved F6 drafts;
- purge is server-only. It checks the F6 draft-reference boundary first, then
  releases the idempotent F10 quota, removes the object, and marks the row
  purged. Referenced assets keep both their object and quota.

The slice deliberately does not invent plan capacities. The server assembly
must provide an F10 command implementation that recognizes
`media_storage_bytes` according to the active commercial configuration. A
missing or failing quota decision blocks uploads.

## HTTP API

All endpoints require authentication and workspace media permission:

| Method | Path | Result |
| --- | --- | --- |
| `POST` | `/api/v1/workspaces/{workspace_id}/media/uploads` | Reserve quota and issue a signed upload |
| `POST` | `/api/v1/workspaces/{workspace_id}/media/uploads/{upload_id}/complete` | Inspect and create the asset |
| `GET` | `/api/v1/workspaces/{workspace_id}/media/assets` | Search ready assets by `q`, `kind`, and comma-separated `tags` |
| `GET` | `/api/v1/workspaces/{workspace_id}/media/assets/{asset_id}` | Read ready or archived metadata |
| `PATCH` | `/api/v1/workspaces/{workspace_id}/media/assets/{asset_id}` | Update searchable metadata with a revision |
| `DELETE` | `/api/v1/workspaces/{workspace_id}/media/assets/{asset_id}?revision=N` | Archive without breaking drafts |
| `GET` | `/api/v1/workspaces/{workspace_id}/media/assets/{asset_id}/composer-reference` | Produce a new-reuse F6 media value |

The full wire contract is in `contracts/openapi.yaml`. Internal F10 and F6
boundary schemas are in `contracts/`.

## Runtime discovery

No central registry is needed. The manifest declares F6 and F10 dependencies,
so configured roots must discover those slices too:

```sh
POSTQRON_FEATURE_ROOTS="services/api/features:features" pnpm migrations:check
```

## Verification

```sh
cd features/f19-media-library
GOWORK=off go test -race ./...
GOWORK=off go vet ./...

cd ../..
POSTQRON_FEATURE_ROOTS="services/api/features:features" pnpm migrations:check
```

Set `F19_DATABASE_URL` after applying the migration to enable the PostgreSQL
repository integration test.
