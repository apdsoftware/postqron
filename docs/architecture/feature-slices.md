# Autonomous feature slices

Every feature owns a `feature.yaml` next to its entrypoint. Runtime discovery
walks configured roots recursively, so adding a feature never requires a
central registry.

```yaml
schema_version: 1
id: publishing
kind: worker
version: 0.1.0
entrypoint: ./worker.go
dependencies:
  - scheduling
migrations:
  - ./migrations/000001_create_attempts.sql
```

## Contract

- `schema_version` is currently `1`.
- `id` is a stable lowercase identifier.
- `kind` is `web`, `api`, or `worker`.
- `version` uses semantic versioning.
- `entrypoint` must be a file inside the feature directory.
- `dependencies` contains other discovered feature IDs.
- `migrations` contains SQL files inside the feature directory.

Both TypeScript and Go runtimes reject unknown manifest fields, duplicate IDs,
missing dependencies, cycles, missing files, absolute paths, and paths escaping
the slice. Dependency order is deterministic.

## Migrations

Migrations are forward-only and stay in `<feature>/migrations`. Names use
`NNNNNN_description.sql`. The runner:

1. discovers manifests;
2. validates names, locality, non-empty SQL, and absence of down sections or
   explicit transaction control;
3. takes a PostgreSQL advisory lock;
4. executes each pending file in its own transaction;
5. records feature ID, name, SHA-256 checksum, and application time.

Changing an applied migration fails instead of silently drifting. Correct an
already-applied schema with a new forward migration.

Validate without PostgreSQL:

```sh
pnpm migrations:check
```

Apply to an explicitly supplied database:

```sh
DATABASE_URL=postgres://... go run ./services/api/cmd/migrate
```

The CI migration job applies all files twice to prove idempotent ledger
behavior.

## Slice checklist

A new slice should keep its manifest, entrypoint, contracts, migrations, and
tests inside the slice directory. It may depend only on IDs declared in its
manifest. Cross-feature behavior should use an explicit API or event contract
owned by the producing slice.
