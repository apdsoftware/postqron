# Architecture foundation

Postqron starts as three independently runnable processes:

```text
Cloudflare -> Caddy -> Nuxt web
                    -> Go API -> PostgreSQL
                              -> durable job state
                         worker -> provider adapters
```

The initial worker only proves process lifecycle and slice discovery. Feature
slices add durable jobs and provider integrations without changing a central
registry.

## Repository boundaries

- `apps/web`: Nuxt 3 shell and web feature slices.
- `services/api`: versioned HTTP API, database migration runner, and API slices.
- `services/worker`: long-running worker process and worker slices.
- `packages/runtime`: TypeScript and Go implementations of the `feature.yaml`
  contract.
- `infra`: local dependencies, production Compose, and Terraform.

The API exposes liveness at `/healthz`, readiness at `/readyz`, and a
non-sensitive feature catalog at `/api/v1/features`. The web shell exposes its
own `/api/health` endpoint. Neither catalog returns filesystem paths.

## Process configuration

Configuration is read from environment variables and validated at startup.
Relevant foundation variables are:

| Variable | Process | Default | Purpose |
| --- | --- | --- | --- |
| `NUXT_PORT` | web dev server | `3000` | Local listening port |
| `NUXT_PUBLIC_API_BASE` | web | `http://localhost:8080` | Browser-visible API origin |
| `API_ADDR` | API | `:8080` | API listen address |
| `POSTQRON_FEATURE_ROOTS` | all | process-specific `features` path | OS-separated discovery roots |
| `WORKER_POLL_INTERVAL` | worker | `5s` | Foundation polling interval |
| `DATABASE_URL` | migrate | none | PostgreSQL DSN; required only when applying |

Production secrets do not have defaults.

## Operational baseline

- Processes run as unprivileged users in their containers.
- HTTP servers have explicit read, write, header, idle, and shutdown timeouts.
- Logs are structured JSON and contain no request bodies, credentials, or local
  feature paths.
- PostgreSQL is not published from the production Compose network.
- Caddy terminates origin TLS and adds baseline security headers; Cloudflare
  proxies both public DNS records.
- Terraform prevents accidental server destruction. A planned replacement
  requires an explicit lifecycle change and reviewed recovery plan.

The Compose database is a deployable foundation, not a high-availability
database topology. Before production traffic, the platform owner must attach
the backup/PITR implementation and restore tests defined by D05.
