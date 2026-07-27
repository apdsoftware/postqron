import {
  priceForChannels,
  type BillingInterval,
  type Money,
  type PricingLocale,
  type PublicCatalog,
  type PublicPlan,
  type PublicPlanCode,
} from './catalog.ts'

// Shared quantity/interval/plan selection model for the public pricing
// surfaces (F2, F34, F29). Every price, limit, and threshold is read from
// the runtime d09-v2 catalog: this module only encodes the selection rules.

export const OVER_MAX_QUANTITY = 'over_max' as const

export type ChannelQuantity = number | typeof OVER_MAX_QUANTITY

// Commercial upgrade order only; prices and limits still come from the catalog.
export const PLAN_ORDER: readonly PublicPlanCode[] = ['start', 'pro', 'team', 'unlimited']

export const MONTHS_OF_SERVICE_PER_YEAR = 12

export interface PricingSelection {
  interval: BillingInterval
  quantity: ChannelQuantity
  explicitPlan: PublicPlanCode | null
}

export interface AnnualBillingTerms {
  monthsCharged: number
  monthsOfService: number
  savingRatio: number
}

export interface CheckoutIntent {
  plan: PublicPlanCode
  interval: BillingInterval
  quantity: number | null
}

export function initialPricingSelection(): PricingSelection {
  return { interval: 'monthly', quantity: 1, explicitPlan: null }
}

function planByCode(catalog: PublicCatalog, code: PublicPlanCode): PublicPlan {
  const plan = catalog.plans.find(candidate => candidate.code === code)
  if (!plan) {
    throw new Error('PRICING_MODEL_PLAN_MISSING')
  }
  return plan
}

export function orderedPlans(catalog: PublicCatalog): PublicPlan[] {
  return PLAN_ORDER.map(code => planByCode(catalog, code))
}

export function maxSelectableChannels(catalog: PublicCatalog): number {
  const finiteLimits = catalog.plans
    .filter(plan => plan.purchasable && plan.limits.channels !== null)
    .map(plan => plan.limits.channels as number)
  if (finiteLimits.length === 0) {
    throw new Error('PRICING_MODEL_NO_CHANNEL_LIMITS')
  }
  return Math.max(...finiteLimits)
}

export function overMaxThreshold(catalog: PublicCatalog): number {
  return maxSelectableChannels(catalog) + 1
}

export function quantityOptions(catalog: PublicCatalog): ChannelQuantity[] {
  return [
    ...Array.from({ length: maxSelectableChannels(catalog) }, (_, index) => index + 1),
    OVER_MAX_QUANTITY,
  ]
}

export function isQuantityAllowed(
  catalog: PublicCatalog,
  quantity: ChannelQuantity,
): boolean {
  if (quantity === OVER_MAX_QUANTITY) {
    return true
  }
  return Number.isSafeInteger(quantity)
    && quantity >= 1
    && quantity <= maxSelectableChannels(catalog)
}

export function isPlanCompatible(plan: PublicPlan, quantity: ChannelQuantity): boolean {
  if (plan.limits.channels === null) {
    return true
  }
  if (quantity === OVER_MAX_QUANTITY) {
    return false
  }
  return quantity <= plan.limits.channels
}

export function compatiblePlans(
  catalog: PublicCatalog,
  quantity: ChannelQuantity,
): PublicPlan[] {
  return orderedPlans(catalog).filter(plan => isPlanCompatible(plan, quantity))
}

export function minimalCompatiblePlan(
  catalog: PublicCatalog,
  quantity: ChannelQuantity,
): PublicPlan {
  const plan = orderedPlans(catalog).find(candidate => isPlanCompatible(candidate, quantity))
  if (!plan) {
    throw new Error('PRICING_MODEL_NO_COMPATIBLE_PLAN')
  }
  return plan
}

export function selectedPlan(
  catalog: PublicCatalog,
  selection: PricingSelection,
): PublicPlan {
  if (selection.explicitPlan !== null) {
    const explicit = planByCode(catalog, selection.explicitPlan)
    if (isPlanCompatible(explicit, selection.quantity)) {
      return explicit
    }
  }
  return minimalCompatiblePlan(catalog, selection.quantity)
}

export function withInterval(
  selection: PricingSelection,
  interval: BillingInterval,
): PricingSelection {
  return { ...selection, interval }
}

export function withQuantity(
  catalog: PublicCatalog,
  selection: PricingSelection,
  quantity: ChannelQuantity,
): PricingSelection {
  if (!isQuantityAllowed(catalog, quantity)) {
    throw new Error('PRICING_MODEL_INVALID_QUANTITY')
  }
  const keepsExplicitChoice = selection.explicitPlan !== null
    && isPlanCompatible(planByCode(catalog, selection.explicitPlan), quantity)
  return {
    ...selection,
    quantity,
    explicitPlan: keepsExplicitChoice ? selection.explicitPlan : null,
  }
}

export function withPlan(
  catalog: PublicCatalog,
  selection: PricingSelection,
  code: PublicPlanCode,
): PricingSelection {
  if (!isPlanCompatible(planByCode(catalog, code), selection.quantity)) {
    throw new Error('PRICING_MODEL_INCOMPATIBLE_PLAN')
  }
  return { ...selection, explicitPlan: code }
}

export function billedChannels(
  plan: PublicPlan,
  quantity: ChannelQuantity,
): number | null {
  if (plan.limits.channels === null) {
    return null
  }
  if (!isPlanCompatible(plan, quantity)) {
    throw new Error('PRICING_MODEL_INCOMPATIBLE_PLAN')
  }
  return plan.purchasable ? quantity as number : plan.limits.channels
}

export function planTotal(
  plan: PublicPlan,
  interval: BillingInterval,
  quantity: ChannelQuantity,
): Money {
  return priceForChannels(plan, interval, billedChannels(plan, quantity))
}

export function perChannelPrice(
  plan: PublicPlan,
  interval: BillingInterval,
  quantity: ChannelQuantity,
): Money | null {
  const channels = billedChannels(plan, quantity)
  if (channels === null || !plan.purchasable) {
    return null
  }
  const total = priceForChannels(plan, interval, channels)
  return {
    amount_cents: Math.round(total.amount_cents / channels),
    currency: total.currency,
  }
}

export function annualBillingTerms(catalog: PublicCatalog): AnnualBillingTerms {
  const paidPlans = catalog.plans
    .filter(plan => plan.purchasable && plan.prices.monthly.amount_cents > 0)
  if (paidPlans.length === 0) {
    throw new Error('PRICING_MODEL_NO_PURCHASABLE_PLAN')
  }
  const terms = paidPlans.map((plan) => {
    const monthly = plan.prices.monthly.amount_cents
    const annual = plan.prices.annual.amount_cents
    const yearAtMonthlyRate = MONTHS_OF_SERVICE_PER_YEAR * monthly
    return {
      monthsCharged: annual / monthly,
      savingRatio: (yearAtMonthlyRate - annual) / yearAtMonthlyRate,
    }
  })
  const [reference] = terms
  if (terms.some(term => Math.abs(term.monthsCharged - reference!.monthsCharged) > 1e-9)) {
    throw new Error('PRICING_MODEL_INCONSISTENT_ANNUAL_TERMS')
  }
  return {
    monthsCharged: reference!.monthsCharged,
    monthsOfService: MONTHS_OF_SERVICE_PER_YEAR,
    savingRatio: reference!.savingRatio,
  }
}

export function annualSaving(plan: PublicPlan, quantity: ChannelQuantity): Money {
  const channels = billedChannels(plan, quantity)
  const monthly = priceForChannels(plan, 'monthly', channels)
  const annual = priceForChannels(plan, 'annual', channels)
  return {
    amount_cents: Math.max(
      0,
      MONTHS_OF_SERVICE_PER_YEAR * monthly.amount_cents - annual.amount_cents,
    ),
    currency: annual.currency,
  }
}

export function formatPercent(ratio: number, locale: PricingLocale): string {
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  }).format(ratio)
}

export function checkoutIntentFor(
  plan: PublicPlan,
  interval: BillingInterval,
  quantity: ChannelQuantity,
): CheckoutIntent {
  return {
    plan: plan.code,
    interval,
    quantity: billedChannels(plan, quantity),
  }
}
