import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  PRICING_COPY,
  PRICING_LOCALES,
  interpolate,
  type PublicCatalog,
} from '../src/catalog.ts'
import {
  OVER_MAX_QUANTITY,
  PLAN_ORDER,
  annualBillingTerms,
  annualSaving,
  billedChannels,
  checkoutIntentFor,
  compatiblePlans,
  formatPercent,
  initialPricingSelection,
  isPlanCompatible,
  isQuantityAllowed,
  maxSelectableChannels,
  minimalCompatiblePlan,
  orderedPlans,
  overMaxThreshold,
  perChannelPrice,
  planTotal,
  quantityOptions,
  selectedPlan,
  withInterval,
  withPlan,
  withQuantity,
  type ChannelQuantity,
} from '../src/pricing-model.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = (path: string) => readFile(resolve(featureRoot, path), 'utf8')

const money = (amount_cents: number) => ({ amount_cents, currency: 'EUR' as const })
const tiers = (first: number, second: number, third: number) => [
  { from_channel: 1, to_channel: 10, monthly: money(first), annual: money(first * 10) },
  { from_channel: 11, to_channel: 25, monthly: money(second), annual: money(second * 10) },
  { from_channel: 26, to_channel: 50, monthly: money(third), annual: money(third * 10) },
]

function buildCatalog(): PublicCatalog {
  return {
    provider: 'paddle',
    catalog_version: 'd09-v2',
    currency: 'EUR',
    plans: [
      {
        code: 'unlimited', name: 'Unlimited', purchasable: true,
        prices: { monthly: money(12_900), annual: money(129_000) },
        price_tiers: [],
        limits: { members: null, channels: null, scheduled_publications: null, scheduled_publications_per_channel: null },
      },
      {
        code: 'start', name: 'Start', purchasable: false,
        prices: { monthly: money(0), annual: money(0) }, price_tiers: [],
        limits: { members: 1, channels: 3, scheduled_publications: 10, scheduled_publications_per_channel: 10 },
      },
      {
        code: 'team', name: 'Team', purchasable: true,
        prices: { monthly: money(900), annual: money(9_000) },
        price_tiers: tiers(900, 300, 225),
        limits: { members: 9, channels: 9, scheduled_publications: 500, scheduled_publications_per_channel: 500 },
        trial: { days: 14, members: 9, channels: 9, scheduled_publications_per_channel: 500 },
      },
      {
        code: 'pro', name: 'Pro', purchasable: true,
        prices: { monthly: money(450), annual: money(4_500) },
        price_tiers: tiers(450, 300, 225),
        limits: { members: 1, channels: 6, scheduled_publications: 500, scheduled_publications_per_channel: 500 },
      },
    ],
  }
}

const catalog = buildCatalog()
const plan = (code: string) => catalog.plans.find(candidate => candidate.code === code)!

test('plans are presented in Start → Pro → Team → Unlimited order regardless of catalog order', () => {
  assert.deepEqual(orderedPlans(catalog).map(entry => entry.code), [...PLAN_ORDER])
})

test('quantity options come from the runtime catalog, not from hardcoded limits', () => {
  assert.equal(maxSelectableChannels(catalog), 9)
  assert.equal(overMaxThreshold(catalog), 10)
  assert.deepEqual(quantityOptions(catalog), [1, 2, 3, 4, 5, 6, 7, 8, 9, OVER_MAX_QUANTITY])

  const widerCatalog = buildCatalog()
  widerCatalog.plans.find(entry => entry.code === 'team')!.limits.channels = 12
  assert.equal(maxSelectableChannels(widerCatalog), 12)
  assert.equal(quantityOptions(widerCatalog).length, 13)
  assert.equal(overMaxThreshold(widerCatalog), 13)
})

test('the default selection is monthly, 1 channel, and the minimal compatible plan', () => {
  const selection = initialPricingSelection()
  assert.equal(selection.interval, 'monthly')
  assert.equal(selection.quantity, 1)
  assert.equal(selection.explicitPlan, null)
  assert.equal(selectedPlan(catalog, selection).code, 'start')
})

test('preselection follows the 1-3, 4-6, 7-9, over-max compatibility windows', () => {
  const expectations: Array<[ChannelQuantity, string]> = [
    [1, 'start'], [2, 'start'], [3, 'start'],
    [4, 'pro'], [5, 'pro'], [6, 'pro'],
    [7, 'team'], [8, 'team'], [9, 'team'],
    [OVER_MAX_QUANTITY, 'unlimited'],
  ]
  let selection = initialPricingSelection()
  for (const [quantity, expected] of expectations) {
    selection = withQuantity(catalog, selection, quantity)
    assert.equal(selectedPlan(catalog, selection).code, expected, `quantity ${String(quantity)}`)
    assert.equal(minimalCompatiblePlan(catalog, quantity).code, expected)
  }
})

test('lower incompatible plans are disabled while higher plans stay selectable', () => {
  assert.deepEqual(compatiblePlans(catalog, 2).map(entry => entry.code), ['start', 'pro', 'team', 'unlimited'])
  assert.deepEqual(compatiblePlans(catalog, 5).map(entry => entry.code), ['pro', 'team', 'unlimited'])
  assert.deepEqual(compatiblePlans(catalog, 8).map(entry => entry.code), ['team', 'unlimited'])
  assert.deepEqual(compatiblePlans(catalog, OVER_MAX_QUANTITY).map(entry => entry.code), ['unlimited'])
  assert.equal(isPlanCompatible(plan('start'), 4), false)
  assert.equal(isPlanCompatible(plan('pro'), 7), false)
  assert.equal(isPlanCompatible(plan('team'), OVER_MAX_QUANTITY), false)
  assert.equal(isPlanCompatible(plan('unlimited'), OVER_MAX_QUANTITY), true)
})

test('an explicit higher plan is kept while compatible and released when it becomes incompatible', () => {
  let selection = withQuantity(catalog, initialPricingSelection(), 2)
  selection = withPlan(catalog, selection, 'team')
  assert.equal(selectedPlan(catalog, selection).code, 'team')

  selection = withQuantity(catalog, selection, 5)
  assert.equal(selection.explicitPlan, 'team')
  assert.equal(selectedPlan(catalog, selection).code, 'team')

  selection = withQuantity(catalog, selection, OVER_MAX_QUANTITY)
  assert.equal(selection.explicitPlan, null)
  assert.equal(selectedPlan(catalog, selection).code, 'unlimited')

  selection = withQuantity(catalog, selection, 5)
  assert.equal(selectedPlan(catalog, selection).code, 'pro')
})

test('an explicit choice of an incompatible plan is rejected', () => {
  const selection = withQuantity(catalog, initialPricingSelection(), 7)
  assert.throws(() => withPlan(catalog, selection, 'start'), /INCOMPATIBLE/)
  assert.throws(() => withPlan(catalog, selection, 'pro'), /INCOMPATIBLE/)
  assert.equal(withPlan(catalog, selection, 'unlimited').explicitPlan, 'unlimited')
})

test('invalid quantities are rejected instead of being silently clamped', () => {
  const selection = initialPricingSelection()
  for (const quantity of [0, -1, 10, 2.5, Number.NaN]) {
    assert.equal(isQuantityAllowed(catalog, quantity), false)
    assert.throws(() => withQuantity(catalog, selection, quantity), /INVALID_QUANTITY/)
  }
  assert.equal(isQuantityAllowed(catalog, OVER_MAX_QUANTITY), true)
})

test('switching the billing interval never changes quantity or selected plan', () => {
  let selection = withQuantity(catalog, initialPricingSelection(), 5)
  selection = withPlan(catalog, selection, 'team')
  const annual = withInterval(selection, 'annual')
  assert.equal(annual.interval, 'annual')
  assert.equal(annual.quantity, 5)
  assert.equal(selectedPlan(catalog, annual).code, 'team')
  assert.equal(withInterval(annual, 'monthly').interval, 'monthly')
})

test('totals and per-channel prices derive from the catalog for the selected quantity', () => {
  assert.equal(planTotal(plan('pro'), 'monthly', 4).amount_cents, 1_800)
  assert.equal(planTotal(plan('pro'), 'annual', 4).amount_cents, 18_000)
  assert.equal(planTotal(plan('team'), 'monthly', 7).amount_cents, 6_300)
  assert.equal(planTotal(plan('team'), 'annual', 7).amount_cents, 63_000)
  assert.equal(perChannelPrice(plan('pro'), 'monthly', 4)!.amount_cents, 450)
  assert.equal(perChannelPrice(plan('team'), 'annual', 7)!.amount_cents, 9_000)
  assert.throws(() => planTotal(plan('pro'), 'monthly', 7), /INCOMPATIBLE/)
})

test('Start keeps its own free capacity and Unlimited ignores the quantity selector', () => {
  assert.equal(billedChannels(plan('start'), 2), 3)
  assert.equal(planTotal(plan('start'), 'monthly', 2).amount_cents, 0)
  assert.equal(perChannelPrice(plan('start'), 'monthly', 2), null)
  assert.equal(billedChannels(plan('unlimited'), 4), null)
  assert.equal(billedChannels(plan('unlimited'), OVER_MAX_QUANTITY), null)
  assert.equal(planTotal(plan('unlimited'), 'monthly', OVER_MAX_QUANTITY).amount_cents, 12_900)
  assert.equal(planTotal(plan('unlimited'), 'annual', 3).amount_cents, 129_000)
  assert.equal(perChannelPrice(plan('unlimited'), 'monthly', 3), null)
})

test('annual terms are derived from the catalog: 10 instalments, 12 months, 16.67% saving', () => {
  const terms = annualBillingTerms(catalog)
  assert.equal(terms.monthsCharged, 10)
  assert.equal(terms.monthsOfService, 12)
  assert.ok(Math.abs(terms.savingRatio - 1 / 6) < 1e-9)
  assert.equal(formatPercent(terms.savingRatio, 'en'), '16.67%')
  assert.equal(formatPercent(terms.savingRatio, 'it'), '16,67%')

  const inconsistent = buildCatalog()
  inconsistent.plans.find(entry => entry.code === 'unlimited')!.prices.annual = money(120_000)
  assert.throws(() => annualBillingTerms(inconsistent), /INCONSISTENT_ANNUAL_TERMS/)
})

test('a single divergent price tier fails the annual terms closed', () => {
  // priceForChannels bills Pro and Team from price_tiers, so a tier whose
  // annual price is 11 monthly instalments must be rejected even when
  // plan.prices still advertises 10.
  const elevenTimes = buildCatalog()
  const pro = elevenTimes.plans.find(entry => entry.code === 'pro')!
  pro.price_tiers[0]!.annual = money(pro.price_tiers[0]!.monthly.amount_cents * 11)
  assert.throws(() => annualBillingTerms(elevenTimes), /INCONSISTENT_ANNUAL_TERMS/)

  const deepTier = buildCatalog()
  const team = deepTier.plans.find(entry => entry.code === 'team')!
  team.price_tiers[2]!.annual = money(team.price_tiers[2]!.monthly.amount_cents * 11)
  assert.throws(() => annualBillingTerms(deepTier), /INCONSISTENT_ANNUAL_TERMS/)

  const freeTier = buildCatalog()
  freeTier.plans.find(entry => entry.code === 'pro')!.price_tiers[1]!.monthly = money(0)
  assert.throws(() => annualBillingTerms(freeTier), /INCONSISTENT_ANNUAL_TERMS/)
})

test('a purchasable plan with a zero monthly base cannot dodge tier validation', () => {
  // With the old base-price filter, Pro would have been excluded entirely
  // and its 11-instalment tier would have gone unchecked.
  const zeroBase = buildCatalog()
  const pro = zeroBase.plans.find(entry => entry.code === 'pro')!
  pro.prices.monthly = money(0)
  pro.price_tiers[0]!.annual = money(pro.price_tiers[0]!.monthly.amount_cents * 11)
  assert.throws(() => annualBillingTerms(zeroBase), /INCONSISTENT_ANNUAL_TERMS/)

  const zeroBaseOnly = buildCatalog()
  zeroBaseOnly.plans.find(entry => entry.code === 'team')!.prices.monthly = money(0)
  assert.throws(() => annualBillingTerms(zeroBaseOnly), /INCONSISTENT_ANNUAL_TERMS/)
})

test('billed annual totals match the advertised instalments at every purchasable quantity', () => {
  const terms = annualBillingTerms(catalog)
  for (const code of ['pro', 'team'] as const) {
    const paid = plan(code)
    for (let quantity = 1; quantity <= paid.limits.channels!; quantity += 1) {
      assert.equal(
        planTotal(paid, 'annual', quantity).amount_cents,
        terms.monthsCharged * planTotal(paid, 'monthly', quantity).amount_cents,
        `${code} at ${quantity} channels`,
      )
    }
  }
  assert.equal(
    planTotal(plan('unlimited'), 'annual', OVER_MAX_QUANTITY).amount_cents,
    terms.monthsCharged * planTotal(plan('unlimited'), 'monthly', OVER_MAX_QUANTITY).amount_cents,
  )
})

test('annual saving equals two monthly instalments for the selected quantity', () => {
  assert.equal(annualSaving(plan('pro'), 4).amount_cents, 3_600)
  assert.equal(annualSaving(plan('team'), 9).amount_cents, 16_200)
  assert.equal(annualSaving(plan('unlimited'), OVER_MAX_QUANTITY).amount_cents, 25_800)
  assert.equal(annualSaving(plan('start'), 1).amount_cents, 0)
})

test('checkout intents keep only valid plan, quantity, and interval combinations', () => {
  assert.deepEqual(checkoutIntentFor(plan('pro'), 'monthly', 4), {
    plan: 'pro', interval: 'monthly', quantity: 4,
  })
  assert.deepEqual(checkoutIntentFor(plan('team'), 'annual', 7), {
    plan: 'team', interval: 'annual', quantity: 7,
  })
  assert.deepEqual(checkoutIntentFor(plan('unlimited'), 'annual', OVER_MAX_QUANTITY), {
    plan: 'unlimited', interval: 'annual', quantity: null,
  })
  assert.throws(() => checkoutIntentFor(plan('pro'), 'monthly', 8), /INCOMPATIBLE/)
  assert.throws(() => checkoutIntentFor(plan('start'), 'monthly', OVER_MAX_QUANTITY), /INCOMPATIBLE/)
})

test('the selector copy exists in all five locales and states the required guarantees', () => {
  const overMaxLabels = {
    en: '10+ social channels',
    it: '10+ canali social',
    es: '10+ canales sociales',
    fr: '10+ canaux sociaux',
    de: '10+ Social-Media-Kanäle',
  } as const

  for (const locale of PRICING_LOCALES) {
    const copy = PRICING_COPY[locale]
    for (const key of [
      'quantityOverMax', 'planGroupLabel', 'selectedPlanAnnouncement', 'annualOption',
      'annualExplainer', 'annualPayForService', 'annualSavingAmount', 'totalForChannel',
      'totalForChannels', 'perChannelMonthly', 'perChannelAnnual', 'usersIncludedOne',
      'usersIncludedMany', 'usersIncludedUnlimited', 'incompatibleChannels',
      'unlimitedFlatIndependent', 'startSelectorNote',
    ] as const) {
      assert.ok(copy[key].length > 0, `${locale}.${key}`)
    }
    assert.match(copy.annualExplainer, /\{months\}/)
    assert.match(copy.annualExplainer, /\{serviceMonths\}/)
    assert.match(copy.annualExplainer, /\{percent\}/)
    assert.doesNotMatch(copy.quantityHelp, /\d/)
    assert.equal(
      interpolate(copy.quantityOverMax, { count: 10 }),
      overMaxLabels[locale],
      `${locale}.quantityOverMax must localize the slider endpoint`,
    )
    for (const parameter of ['{plan}', '{quantity}', '{interval}', '{total}']) {
      assert.ok(
        copy.selectedPlanAnnouncement.includes(parameter),
        `${locale}.selectedPlanAnnouncement must announce ${parameter}`,
      )
    }
  }

  const terms = annualBillingTerms(catalog)
  assert.equal(
    interpolate(PRICING_COPY.it.annualExplainer, {
      months: terms.monthsCharged,
      serviceMonths: terms.monthsOfService,
      percent: formatPercent(terms.savingRatio, 'it'),
    }),
    'Con la fatturazione annuale paghi anticipatamente 10 mensilità e utilizzi il servizio per 12 mesi. Risparmi il 16,67% rispetto al mensile.',
  )
  assert.equal(
    interpolate(PRICING_COPY.it.annualOption, {
      months: terms.monthsCharged,
      serviceMonths: terms.monthsOfService,
    }),
    'Annuale — paghi 10 mesi su 12',
  )
  assert.equal(PRICING_COPY.it.unlimitedFlatIndependent, 'Prezzo fisso, indipendente dal numero di canali')
})

test('the pricing component consumes the shared model without duplicating rules', async () => {
  const component = await source('components/PlanCatalog.vue')
  assert.match(component, /from '~\/src\/pricing-model'/)
  assert.match(component, /initialPricingSelection\(\)/)
  assert.match(component, /role="radiogroup"/)
  assert.match(component, /role="group"/)
  assert.match(component, /type="radio"/)
  assert.match(component, /:disabled="!compatible\(plan\)"/)
  assert.match(component, /aria-live="polite"/)
  assert.match(component, /selectionAnnouncement/)
  assert.match(component, /aria-describedby/)
  assert.match(component, /plan-incompatible-\$\{plan\.code\}/)
  assert.match(component, /checkoutIntentFor/)
  assert.match(component, /type="range"/)
  assert.match(component, /min="1"/)
  assert.match(component, /:max="sliderMaximum"/)
  assert.match(component, /step="1"/)
  assert.match(component, /:aria-valuetext="quantityOptionLabel\(selection\.quantity\)"/)
  assert.match(component, /@input="setQuantity/)
  assert.match(component, /quantity-control__markers/)
  assert.match(component, /sliderMarkers/)
  assert.doesNotMatch(component, /<select/)
  assert.doesNotMatch(component, /channels = ref\(3\)/)
  assert.doesNotMatch(component, /amount_cents:\s*\d/)
})

test('disabled plan cards stay distinct without attenuating text contrast', async () => {
  const component = await source('components/PlanCatalog.vue')
  const disabledRule = component.match(/\.plan-card--disabled\s*\{[^}]*\}/u)?.[0]
  assert.ok(disabledRule, 'PlanCatalog must style the disabled plan card state')
  // Opacity composites token colors below the WCAG AA 4.5:1 contrast ratio
  // (QA measured 2.55:1 at 55% opacity); the disabled state must rely on
  // non-text cues instead.
  assert.doesNotMatch(component, /plan-card--disabled[^}]*opacity/u)
  assert.doesNotMatch(disabledRule!, /opacity|filter|color:/u)
  assert.match(disabledRule!, /border-style: dashed/)
  assert.match(disabledRule!, /--pq-color-surface-subtle/)
})
