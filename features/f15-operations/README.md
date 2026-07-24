# F15 — Operations

This autonomous slice implements Postqron's operational observability and
security controls without a central registry.

## Controls

- `NewRedactingHandler` wraps every structured `slog` destination and removes
  secret/PII fields plus common credential patterns before emission.
- `HealthHandler` exposes liveness, dependency-aware readiness, and bounded
  Prometheus metrics. `/metrics` is defined as an internal mTLS endpoint.
- `config/alerts.yaml` pages on delayed publication queues, failed
  publications, readiness, audit persistence, stale backups, and overdue
  restore tests. Every alert links to an owned runbook.
- `RateLimiter` implements a bounded token bucket and HTTP `429` response.
  Integrations must derive keys from authenticated server context or a trusted
  edge, never directly from a spoofable header. Multi-replica deployments use a
  shared central store.
- `SecretManager` is the sole secret lookup boundary: names are allowlisted and
  values come from an injected provider. The map provider is for tests/local
  development only; staging and production use the deployment secret manager.
- `AuditRecorder` validates a fixed, payload-free event schema and fails closed
  when the append-only sink is unavailable. The migration rejects updates and
  deletes and exposes a restricted purge function for the D05 12-month
  retention job. Runtime roles receive `INSERT`/restricted `SELECT`, never table
  ownership or direct `UPDATE`/`DELETE`; only the retention role may execute the
  purge function.
- `config/backup-policy.yaml` and the backup/restore runbook encode the adopted
  15-minute database RPO, 4-hour database RTO, 8-hour end-to-end RTO, 35-day
  backup retention, and required restore drills.

No secret values, raw personal data, post content, provider payloads, or
high-cardinality tenant/user labels belong in logs, metrics, or alerts.

## Discovery and validation

API hosts include this directory in `POSTQRON_FEATURE_ROOTS`; F16 discovers
`feature.yaml` recursively and applies the forward-only audit migration.

Run the slice tests:

```sh
cd features/f15-operations
GOWORK=off go test -race ./...
```

Validate the manifest and migration with the F16 runtime:

```sh
go run ./services/api/cmd/migrate \
  --check \
  --roots features/f15-operations
```
