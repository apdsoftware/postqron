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
  catalog_version: 'd07-v1',
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
      limits: { members: 1, channels: 50, scheduled_publications: 500, scheduled_publications_per_channel: 500 },
    },
    {
      code: 'team', name: 'Team', purchasable: true,
      prices: { monthly: money(900), annual: money(9000) },
      price_tiers: tiers(900, 300, 225),
      limits: { members: 15, channels: 50, scheduled_publications: 500, scheduled_publications_per_channel: 500 },
      trial: { days: 14, members: 15, channels: 10, scheduled_publications_per_channel: 500 },
    },
  ],
}

test('validates the server-owned Paddle D07 catalog without fallback prices', () => {
  assert.deepEqual(parsePublicCatalog(catalog), catalog)
  const withoutFreeTiers = structuredClone(catalog) as unknown as {
    plans: Array<Record<string, unknown>>
  }
  delete withoutFreeTiers.plans[0]!.price_tiers
  assert.deepEqual(parsePublicCatalog(withoutFreeTiers).plans[0]!.price_tiers, [])
  assert.throws(() => parsePublicCatalog(undefined), /UNAVAILABLE/)
  assert.throws(() => parsePublicCatalog({ ...catalog, provider: 'stripe' }), /INVALID/)
  assert.throws(() => parsePublicCatalog({ ...catalog, plans: catalog.plans.slice(1) }), /INVALID/)
})

test('progressive monthly and annual totals match D07 at every tier edge', () => {
  const pro = catalog.plans[1]!
  const team = catalog.plans[2]!
  assert.equal(priceForChannels(pro, 'monthly', 10).amount_cents, 4_500)
  assert.equal(priceForChannels(pro, 'monthly', 25).amount_cents, 9_000)
  assert.equal(priceForChannels(pro, 'annual', 50).amount_cents, 146_250)
  assert.equal(priceForChannels(team, 'monthly', 50).amount_cents, 19_125)
  assert.equal(monthlyEquivalent(priceForChannels(pro, 'annual', 1), 'annual').amount_cents, 375)
})

test('every paid quantity stays no more expensive than its Buffer equivalent', () => {
  for (const plan of catalog.plans.slice(1)) {
    for (const interval of ['monthly', 'annual'] as const) {
      for (let channels = 1; channels <= 50; channels += 1) {
        assert.ok(
          priceForChannels(plan!, interval, channels).amount_cents
          <= bufferBenchmark(plan!.code as 'pro' | 'team', interval, channels).amount_cents,
        )
        assert.ok(savingsAgainstBuffer(plan!, interval, channels).amount_cents >= 0)
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
    purchaseHref('/app', 'it', catalog.plans[1]!, 'annual', 25),
    '/it/app?plan=pro&interval=annual&quantity=25',
  )
})
