import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import {
  monthlyEquivalent,
  type PublicCatalog,
} from '../../f02-marketing-site/src/catalog.ts'
import {
  OVER_MAX_QUANTITY,
  annualBillingTerms,
  initialPricingSelection,
  planTotal,
  quantityOptions,
  selectedPlan,
  withInterval,
  withPlan,
  withQuantity,
  type ChannelQuantity,
} from '../../f02-marketing-site/src/pricing-model.ts'

const source = (path: string) => readFile(new URL(path, import.meta.url), 'utf8')
const money = (amount_cents: number) => ({
  amount_cents,
  currency: 'EUR' as const,
})
const tiers = (monthly: number) => [{
  from_channel: 1,
  to_channel: 9,
  monthly: money(monthly),
  annual: money(monthly * 10),
}]

function catalog(): PublicCatalog {
  return {
    provider: 'paddle',
    catalog_version: 'd09-v2',
    currency: 'EUR',
    plans: [
      {
        code: 'team',
        name: 'Team',
        purchasable: true,
        prices: { monthly: money(900), annual: money(9_000) },
        price_tiers: tiers(900),
        limits: {
          members: 9,
          channels: 9,
          scheduled_publications: 4_500,
          scheduled_publications_per_channel: 500,
        },
      },
      {
        code: 'unlimited',
        name: 'Unlimited',
        purchasable: true,
        prices: { monthly: money(12_900), annual: money(129_000) },
        price_tiers: [],
        limits: {
          members: null,
          channels: null,
          scheduled_publications: null,
          scheduled_publications_per_channel: null,
        },
      },
      {
        code: 'start',
        name: 'Start',
        purchasable: false,
        prices: { monthly: money(0), annual: money(0) },
        price_tiers: [],
        limits: {
          members: 1,
          channels: 3,
          scheduled_publications: 30,
          scheduled_publications_per_channel: 10,
        },
      },
      {
        code: 'pro',
        name: 'Pro',
        purchasable: true,
        prices: { monthly: money(450), annual: money(4_500) },
        price_tiers: tiers(450),
        limits: {
          members: 1,
          channels: 6,
          scheduled_publications: 3_000,
          scheduled_publications_per_channel: 500,
        },
      },
    ],
  }
}

test('F34 defaults to monthly, one channel and the minimum compatible plan', () => {
  const runtimeCatalog = catalog()
  const selection = initialPricingSelection()
  assert.equal(selection.interval, 'monthly')
  assert.equal(selection.quantity, 1)
  assert.equal(selectedPlan(runtimeCatalog, selection).code, 'start')
  assert.deepEqual(
    quantityOptions(runtimeCatalog),
    [1, 2, 3, 4, 5, 6, 7, 8, 9, OVER_MAX_QUANTITY],
  )
})

test('F34 compatibility follows Start → Pro → Team → Unlimited', () => {
  const runtimeCatalog = catalog()
  const expectations: Array<[ChannelQuantity, string]> = [
    [1, 'start'],
    [3, 'start'],
    [4, 'pro'],
    [6, 'pro'],
    [7, 'team'],
    [9, 'team'],
    [OVER_MAX_QUANTITY, 'unlimited'],
  ]
  for (const [quantity, plan] of expectations) {
    const selection = withQuantity(
      runtimeCatalog,
      initialPricingSelection(),
      quantity,
    )
    assert.equal(selectedPlan(runtimeCatalog, selection).code, plan)
  }
})

test('F34 retains a higher compatible choice and interval changes preserve it', () => {
  const runtimeCatalog = catalog()
  let selection = withPlan(runtimeCatalog, initialPricingSelection(), 'team')
  selection = withQuantity(runtimeCatalog, selection, 4)
  selection = withInterval(selection, 'annual')
  assert.equal(selection.quantity, 4)
  assert.equal(selection.interval, 'annual')
  assert.equal(selectedPlan(runtimeCatalog, selection).code, 'team')

  selection = withQuantity(runtimeCatalog, selection, OVER_MAX_QUANTITY)
  assert.equal(selectedPlan(runtimeCatalog, selection).code, 'unlimited')
  assert.equal(selection.explicitPlan, null)
})

test('F34 annual totals and equivalents are calculated from runtime money', () => {
  const runtimeCatalog = catalog()
  const pro = runtimeCatalog.plans.find(plan => plan.code === 'pro')!
  const terms = annualBillingTerms(runtimeCatalog)
  const total = planTotal(pro, 'annual', 4)
  assert.equal(terms.monthsCharged, 10)
  assert.equal(terms.monthsOfService, 12)
  assert.equal(total.amount_cents, 18_000)
  assert.equal(monthlyEquivalent(total, 'annual').amount_cents, 1_500)
})

test('F34 fails closed when a runtime price tier contradicts annual terms', () => {
  const runtimeCatalog = catalog()
  const pro = runtimeCatalog.plans.find(plan => plan.code === 'pro')!
  pro.price_tiers[0]!.annual = money(
    pro.price_tiers[0]!.monthly.amount_cents * 11,
  )
  assert.throws(
    () => annualBillingTerms(runtimeCatalog),
    /INCONSISTENT_ANNUAL_TERMS/u,
  )
})

test('the pre-launch component consumes the shared model and never builds checkout intent', async () => {
  const component = await source('../components/PrelaunchPricing.vue')
  assert.match(component, /f02-marketing-site\/src\/pricing-model\.ts/u)
  assert.match(component, /initialPricingSelection\(\)/u)
  assert.match(component, /orderedPlans\(props\.catalog\)/u)
  assert.match(component, /withQuantity/u)
  assert.match(component, /withPlan/u)
  assert.match(component, /withInterval/u)
  assert.match(component, /type="range"/u)
  assert.match(component, /v-model\.number="sliderPosition"/u)
  assert.match(component, /:aria-valuetext="quantityValueText\(selection\.quantity\)"/u)
  assert.match(component, /prelaunch-pricing__markers/u)
  assert.match(component, /positionedThresholdMarkers/u)
  assert.match(component, /sliderMarkerPosition/u)
  assert.match(component, /pricing\.sliderGuide/u)
  assert.match(component, /role="radiogroup"/u)
  assert.match(component, /type="radio"/u)
  assert.match(component, /:disabled="!compatible\(plan\)"/u)
  assert.match(component, /selectionAnnouncement/u)
  assert.match(component, /aria-describedby/u)
  assert.match(component, /prelaunch-plan-incompatible-/u)
  assert.match(component, /v-if="compatible\(plan\)"[\s\S]*:to="props\.accessUrl"/u)
  assert.match(component, /annualBillingTerms/u)
  assert.match(component, /planTotal/u)
  assert.match(component, /perChannelPrice/u)
  assert.doesNotMatch(component, /checkoutIntentFor|purchaseHref/u)
  assert.doesNotMatch(component, /type="select"|<select/u)
  assert.doesNotMatch(component, /Math\.min\(3/u)
  assert.doesNotMatch(component, /amount_cents:\s*\d/u)
})

test('the landing validates the catalog against the shared model before rendering controls', async () => {
  const page = await source('../pages/prelaunch.vue')
  assert.match(page, /parsePublicCatalog/u)
  assert.match(page, /orderedPlans\(parsed\)/u)
  assert.match(page, /quantityOptions\(parsed\)/u)
  assert.match(page, /annualBillingTerms\(parsed\)/u)
  assert.match(page, /catch \{[\s\S]*return undefined/u)
})
