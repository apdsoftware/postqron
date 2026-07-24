# Postqron

Postqron is a social-content scheduling SaaS. This repository is a monorepo with
an independently runnable Nuxt shell, Go API, and Go worker.

## Requirements

- Node.js 22.12 or newer
- pnpm 11
- Go 1.26
- Docker (optional, for PostgreSQL)
- Terraform 1.10 or newer (only for infrastructure work)

## Start locally

```sh
pnpm install
go work sync
pnpm dev
```

The defaults are:

- web: `http://localhost:3000`
- API: `http://localhost:8080`
- API health: `http://localhost:8080/healthz`

Override `NUXT_PORT`, `API_ADDR`, and `NUXT_PUBLIC_API_BASE` when running more
than one checkout. Conductor does this automatically using its allocated port
range.

The API and worker start without external services. Start PostgreSQL only when
applying migrations:

```sh
docker compose -f infra/compose/compose.yaml up -d
DATABASE_URL=postgres://postqron:postqron@localhost:5432/postqron?sslmode=disable \
  go run ./services/api/cmd/migrate
```

The password above belongs only to the disposable local Compose environment.
Staging and production secrets are supplied by their runtime secret store and
are never committed.

## Verification

```sh
make verify
```

This runs linting, TypeScript checks, unit tests, migration validation, and
production builds. Infrastructure can be validated separately with:

```sh
terraform -chdir=infra/terraform/environments/staging init -backend=false
terraform -chdir=infra/terraform/environments/staging validate
docker compose -f infra/deploy/compose.yaml config
```

## Feature slices

Each runtime discovers `feature.yaml` files recursively. A slice owns its
entrypoint, contracts, declared dependencies, migrations, and tests. Adding a
slice does not require editing a central registry.

See [docs/architecture/feature-slices.md](docs/architecture/feature-slices.md)
and [docs/architecture/delivery.md](docs/architecture/delivery.md).
