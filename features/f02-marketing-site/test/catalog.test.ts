import assert from 'node:assert/strict'
import test from 'node:test'
import {
  formatMoney,
  monthlyPrice,
  parsePublicCatalog,
  type PublicCatalog,
} from '../src/catalog.ts'

const catalog: PublicCatalog = {
  provider: 'stripe',
  currency: 'EUR',
  plans: [
    {
      code: 'start',
      name: 'Start',
      prices: {
        monthly: { amount_cents: 900, currency: 'EUR' },
        annual: { amount_cents: 9000, currency: 'EUR' },
      },
      limits: { members: 1, channels: 5, scheduled_publications: 100 },
    },
    {
      code: 'pro',
      name: 'Pro',
      prices: {
        monthly: { amount_cents: 2400, currency: 'EUR' },
        annual: { amount_cents: 24000, currency: 'EUR' },
      },
      limits: { members: 5, channels: 15, scheduled_publications: 500 },
    },
    {
      code: 'team',
      name: 'Team',
      prices: {
        monthly: { amount_cents: 4900, currency: 'EUR' },
        annual: { amount_cents: 49000, currency: 'EUR' },
      },
      limits: { members: 15, channels: 50, scheduled_publications: 2000 },
    },
  ],
}

test('the F10 public catalog is validated without changing its values', () => {
  assert.equal(parsePublicCatalog(catalog), catalog)
  assert.equal(formatMoney(catalog.plans[1]!.prices.monthly), '24 €')
  assert.deepEqual(monthlyPrice(catalog.plans[1]!, 'annual'), {
    amount_cents: 2000,
    currency: 'EUR',
  })
})

test('invalid, incomplete, or duplicated catalogs never become fallback prices', () => {
  assert.throws(() => parsePublicCatalog(undefined), /non è disponibile/)
  assert.throws(
    () => parsePublicCatalog({ ...catalog, plans: catalog.plans.slice(0, 2) }),
    /non è valido/,
  )
  assert.throws(
    () => parsePublicCatalog({
      ...catalog,
      plans: [catalog.plans[0], catalog.plans[0], catalog.plans[2]],
    }),
    /duplicati/,
  )
})
