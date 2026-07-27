import assert from 'node:assert/strict'
import test from 'node:test'
import {
  PRICING_COPY,
  PRICING_LOCALES,
  bufferBenchmark,
  formatMoney,
  localeFromPath,
  monthlyEquivalent,
  parsePublicCatalog,
  priceForChannels,
  purchaseHref,
  savingsAgainstBuffer,
  type PublicCatalog,
} from '../src/catalog.ts'

const money = (amount_cents: number) => ({ amount_cents, currency: 'EUR' as const })
const tiers = (first: number, second: number, third: number) => [
  { from_channel: 1, to_channel: 10, monthly: money(first), annual: money(first * 10) },
  { from_channel: 11, to_channel: 25, monthly: money(second), annual: money(second * 10) },
  { from_channel: 26, to_channel: 50, monthly: money(third), annual: money(third * 10) },
]
const catalog: PublicCatalog = {
  provider: 'paddle',
  catalog_version: 'd09-v2',
  currency: 'EUR',
  plans: [
    {
      code: 'start', name: 'Start', purchasable: false,
      prices: { monthly: money(0), annual: money(0) }, price_tiers: [],
      limits: { members: 1, channels: 3, scheduled_publications: 10, scheduled_publications_per_channel: 10 },
    },
    {
      code: 'pro', name: 'Pro', purchasable: true,
      prices: { monthly: money(450), annual: money(4500) },
      price_tiers: tiers(450, 300, 225),
      limits: { members: 3, channels: 6, scheduled_publications: 250, scheduled_publications_per_channel: 250 },
    },
    {
      code: 'team', name: 'Team', purchasable: true,
      prices: { monthly: money(900), annual: money(9000) },
      price_tiers: tiers(900, 300, 225),
      limits: { members: 6, channels: 9, scheduled_publications: 500, scheduled_publications_per_channel: 500 },
      trial: { days: 14, members: 6, channels: 9, scheduled_publications_per_channel: 500 },
    },
    {
      code: 'unlimited', name: 'Unlimited', purchasable: true,
      prices: { monthly: money(12_900), annual: money(129_000) },
      price_tiers: [],
      limits: { members: null, channels: null, scheduled_publications: null, scheduled_publications_per_channel: null },
    },
  ],
}

test('validates the server-owned Paddle D09 catalog without fallback prices', () => {
  assert.deepEqual(parsePublicCatalog(catalog), catalog)
  const withoutFreeTiers = structuredClone(catalog) as unknown as {
    plans: Array<Record<string, unknown>>
  }
  delete withoutFreeTiers.plans[0]!.price_tiers
  assert.deepEqual(parsePublicCatalog(withoutFreeTiers).plans[0]!.price_tiers, [])
  assert.throws(() => parsePublicCatalog(undefined), /UNAVAILABLE/)
  assert.throws(() => parsePublicCatalog({ ...catalog, provider: 'stripe' }), /INVALID/)
  assert.throws(() => parsePublicCatalog({ ...catalog, catalog_version: 'd09-v1' }), /INVALID/)
  assert.throws(() => parsePublicCatalog({ ...catalog, plans: catalog.plans.slice(1) }), /INVALID/)
  assert.throws(() => parsePublicCatalog({
    ...catalog,
    plans: [...catalog.plans, catalog.plans[3]],
  }), /INVALID/)
})

test('plan limits reflect the Product Owner d09-v2 decision without local overrides', () => {
  const parsed = parsePublicCatalog(catalog)
  assert.deepEqual(
    parsed.plans.map(plan => ({
      code: plan.code,
      members: plan.limits.members,
      channels: plan.limits.channels,
      scheduled: plan.limits.scheduled_publications_per_channel,
    })),
    [
      { code: 'start', members: 1, channels: 3, scheduled: 10 },
      { code: 'pro', members: 3, channels: 6, scheduled: 250 },
      { code: 'team', members: 6, channels: 9, scheduled: 500 },
      { code: 'unlimited', members: null, channels: null, scheduled: null },
    ],
  )
})

test('progressive monthly and annual totals match D09 at every tier edge', () => {
  const pro = catalog.plans[1]!
  const team = catalog.plans[2]!
  assert.equal(priceForChannels(pro, 'monthly', 6).amount_cents, 2_700)
  assert.equal(priceForChannels(pro, 'annual', 6).amount_cents, 27_000)
  assert.equal(priceForChannels(team, 'monthly', 9).amount_cents, 8_100)
  assert.equal(priceForChannels(team, 'annual', 9).amount_cents, 81_000)
  assert.equal(monthlyEquivalent(priceForChannels(pro, 'annual', 1), 'annual').amount_cents, 375)
})

test('Unlimited is flat-priced and rejects any channel quantity', () => {
  const unlimited = catalog.plans[3]!
  assert.equal(priceForChannels(unlimited, 'monthly', null).amount_cents, 12_900)
  assert.equal(priceForChannels(unlimited, 'annual', null).amount_cents, 129_000)
  assert.throws(() => priceForChannels(unlimited, 'monthly', 1), /INVALID_QUANTITY/)
  assert.equal(savingsAgainstBuffer(unlimited, 'monthly', 1).amount_cents, 0)
})

test('every paid quantity stays no more expensive than its Buffer equivalent', () => {
  for (const plan of [catalog.plans[1]!, catalog.plans[2]!]) {
    for (const interval of ['monthly', 'annual'] as const) {
      for (let channels = 1; channels <= plan.limits.channels!; channels += 1) {
        assert.ok(
          priceForChannels(plan, interval, channels).amount_cents
          <= bufferBenchmark(plan.code as 'pro' | 'team', interval, channels).amount_cents,
        )
        assert.ok(savingsAgainstBuffer(plan, interval, channels).amount_cents >= 0)
      }
    }
  }
})

test('locale formatting, fallback, and purchase intent are complete', () => {
  const keys = Object.keys(PRICING_COPY.en).sort()
  for (const locale of PRICING_LOCALES) {
    assert.deepEqual(Object.keys(PRICING_COPY[locale]).sort(), keys)
    assert.ok(Object.values(PRICING_COPY[locale]).every(Boolean))
    assert.match(formatMoney(money(1_462_50), locale), /1/)
  }
  assert.equal(localeFromPath('/unknown/prezzi'), 'en')
  assert.equal(
    purchaseHref('/app', 'it', catalog.plans[1]!, 'annual', 6),
    '/it/app?plan=pro&interval=annual&quantity=6',
  )
  assert.equal(
    purchaseHref('/app', 'it', catalog.plans[3]!, 'annual', null),
    '/it/app?plan=unlimited&interval=annual',
  )
})
