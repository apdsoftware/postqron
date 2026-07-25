# F10 entitlements

This autonomous API slice implements the D07 commercial catalog and D06 quota
semantics:

- permanently free Start (3 channels, 1 member, 10 concurrent posts per
  channel);
- progressive Pro and Team pricing in EUR for 1–50 channels, monthly or
  annual, with the annual total equal to ten monthly totals;
- a one-time, cardless 14-day Team trial for 10 channels;
- Paddle-only checkout, subscription changes, cancellation, and temporary
  customer portal sessions;
- exact server-side validation of the expected Paddle product/price line items;
- raw-body `Paddle-Signature` verification, event deduplication, and
  `occurred_at` ordering;
- a 14-day grace period that is anchored to the first failed renewal;
- conservative downgrade and restriction behavior: counters and user resources
  are never deleted.

Client checkout notifications never grant access. Only a verified
`transaction.completed` webhook whose line items exactly match the versioned
server catalog can activate or change a paid entitlement.

The private enforcement override is deliberately absent from Go catalog types,
HTTP routes, OpenAPI schemas, and public database views. Its administrative
assignment and audit workflow belongs to F11.

## Configuration

The runtime reads server-only values from:

- `PADDLE_ENVIRONMENT`: `sandbox` or `production`;
- `PADDLE_API_KEY`: a current-format key for the selected environment;
- `PADDLE_WEBHOOK_SECRET`: the notification destination secret;
- `PADDLE_CATALOG_JSON`: the twelve D07 mappings (Pro/Team × monthly/annual ×
  three progressive tiers).

Each mapping has `plan`, `interval`, `tier`, `product_id`, `price_id`, and
`unit_amount_cents`. Validation rejects missing mappings, reused price IDs,
multiple products for one plan, a shared Pro/Team product, environment/key
mismatches, and amounts that drift from D07. API keys and webhook secrets are
never serialized into public contracts or provider errors.

## Catalog dry-run

The check is read-only. It fetches each configured Paddle price and verifies its
product, active status, EUR amount, and billing interval without changing the
local database or Paddle:

```sh
cd features/f10-entitlements
GOWORK=off go run ./cmd/paddle-catalog-check
```

Run it once with sandbox credentials and once with production credentials
before a catalog cutover.

## Runtime discovery

No central registry is needed. Include both the platform root and this slice:

```sh
POSTQRON_FEATURE_ROOTS="services/api/features:features/f10-entitlements"
```

The same roots validate both forward-only F10 migrations:

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

Set `TEST_DATABASE_URL` to enable PostgreSQL integration tests. Set the Paddle
sandbox variables above to enable opt-in sandbox lifecycle tests.
