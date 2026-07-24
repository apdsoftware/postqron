# F10 entitlements

This autonomous API slice implements D03 and the D06 quota semantics:

- Start, Pro, and Team in EUR with monthly and annual Stripe prices;
- one-time 14-day Pro trial provisioning;
- Owner-only Checkout and Customer Portal orchestration with server-configured
  price IDs and server-recorded Stripe customer bindings;
- signed Stripe webhooks, an event ledger, stale-event protection, and
  activation only after `invoice.paid`;
- atomic member, channel, and scheduled-publication quota commands;
- a client-safe plan overview with usage, remaining capacity, and downgrade
  overage visibility;
- conservative downgrade/restriction behavior: counters and user resources
  are never deleted.

The private enforcement override is deliberately absent from Go catalog types,
HTTP routes, OpenAPI schemas, and public database views. Its administrative
assignment and audit workflow belongs to F11.

## Runtime discovery

No central registry is needed. Include both the platform root and this slice:

```sh
POSTQRON_FEATURE_ROOTS="services/api/features:features/f10-entitlements"
```

The same roots validate its forward-only migration:

```sh
go run ./services/api/cmd/migrate \
  --roots "services/api/features:features/f10-entitlements" \
  --check
```

## Tests

```sh
cd features/f10-entitlements
GOWORK=off go test ./...
```

Set `TEST_DATABASE_URL` to enable PostgreSQL integration tests.
