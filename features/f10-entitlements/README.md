# F10 entitlements

This autonomous API slice implements the D09 v2 public entitlement contract,
including the Product Owner limit decision of 2026-07-27, and D06 quota
semantics:

- permanently free Start (3 channels, 1 member, 10 concurrent posts per
  channel);
- Pro (3 members, 1–6 channels, 250 concurrent posts per channel) and Team
  (6 members, 1–9 channels, 500 concurrent posts per channel), monthly or
  annual, with every annual total equal to ten monthly totals;
- public, flat-priced Unlimited at €129/month or €1,290/year, represented by
  nullable quotas rather than numeric sentinels;
- a one-time, cardless 14-day Team trial for 9 channels and 6 members, distinct
  from payment recovery;
- Paddle-only checkout, subscription changes, cancellation, and temporary
  customer portal sessions;
- Owner-only plan-change previews and requests, with a durable
  `dispatching`/`pending` record that leaves the active entitlement unchanged
  until a matching signed Paddle webhook is applied;
- authoritative downgrade checks for members, channels, and scheduled
  publications; every overage is returned as `resource`, `used`, `limit`, and
  `excess`, and no workspace resource is deleted or silently suspended;
- workspace-level serialization shared with quota reservations, local
  idempotent replay, one active change at a time, stale-event rejection, and a
  second guard at webhook application;
- exact server-side validation of the expected Paddle product/price line items;
- raw-body `Paddle-Signature` verification, event deduplication, and
  `occurred_at` ordering;
- a 30-day Paddle dunning window anchored to the first failed renewal;
- provider-driven suspension and termination: only verified Paddle `paused`
  and `canceled` events restrict service or return a workspace to Start;
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
- `PADDLE_CATALOG_JSON`: the fourteen D09 v1 mappings (the twelve retained D07
  Pro/Team tier mappings plus Unlimited × monthly/annual × flat).

Each mapping has `plan`, `interval`, `tier`, `product_id`, `price_id`, and
`unit_amount_cents`. Validation rejects missing mappings, reused price IDs,
multiple products for one plan, shared paid-plan products, environment/key
mismatches, and amounts that drift from the unchanged D09 v1 Paddle price
catalog. API keys and webhook secrets are
never serialized into public contracts or provider errors.

## Catalog dry-run

The check is read-only. It fetches each configured Paddle price and verifies its
product, active status, EUR amount, billing interval, location-based tax mode,
absence of a Paddle trial, and D09 v1 quantity bounds without changing the local
database or Paddle:

```sh
cd features/f10-entitlements
GOWORK=off go run ./cmd/paddle-catalog-check
```

Run it once with sandbox credentials and once with production credentials
before a catalog cutover.

## Catalog provisioning

The public entitlement response is versioned `d09-v2`. Paddle products, prices,
and provisioning remain on `d09-v1`; `infra/paddle/catalog-d09-v1.json` is that
versioned desired provider catalog and contains
no credentials or provider IDs. The provisioner accepts IDs only from Paddle
responses, is read-only by default, and creates missing marked resources only
when `--apply` is explicit:

```sh
cd features/f10-entitlements
GOWORK=off go run ./cmd/paddle-catalog-provision \
  --manifest ../../infra/paddle/catalog-d09-v1.json
```

Use the protected `Paddle catalog` GitHub workflow for real environments and
follow `docs/operations/paddle-catalog.md`. Its apply artifact contains the
provider-generated mapping to transfer into the matching environment's
`PADDLE_CATALOG_JSON` secret.

## Runtime discovery

No central registry is needed. Include both the platform root and this slice:

```sh
POSTQRON_FEATURE_ROOTS="services/api/features:features/f10-entitlements"
```

The same roots validate all seven forward-only F10 migrations:

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

Set `TEST_DATABASE_URL` to enable PostgreSQL integration tests. Live Paddle
catalog and lifecycle evidence is collected only through the protected workflow
and the operations runbook.

The PostgreSQL suite covers exact downgrade boundaries and overages for all
three resources, concurrent quota reservation versus downgrade, idempotent
request replay before and after the webhook, signed webhook application, and
forward-migration replay.
