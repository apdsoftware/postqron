export type BillingInterval = 'monthly' | 'annual'

export interface Money {
  amount_cents: number
  currency: 'EUR'
}

export interface PublicPlan {
  code: 'start' | 'pro' | 'team'
  name: string
  prices: Record<BillingInterval, Money>
  limits: {
    members: number
    channels: number
    scheduled_publications: number
  }
}

export interface PublicCatalog {
  provider: 'stripe'
  currency: 'EUR'
  plans: PublicPlan[]
}

const PLAN_CODES = new Set(['start', 'pro', 'team'])

function isMoney(value: unknown): value is Money {
  if (!value || typeof value !== 'object') {
    return false
  }
  const money = value as Record<string, unknown>
  return Number.isInteger(money.amount_cents)
    && Number(money.amount_cents) >= 0
    && money.currency === 'EUR'
}

function isPlan(value: unknown): value is PublicPlan {
  if (!value || typeof value !== 'object') {
    return false
  }
  const plan = value as Record<string, unknown>
  const prices = plan.prices as Record<string, unknown> | undefined
  const limits = plan.limits as Record<string, unknown> | undefined
  return typeof plan.code === 'string'
    && PLAN_CODES.has(plan.code)
    && typeof plan.name === 'string'
    && plan.name.length > 0
    && isMoney(prices?.monthly)
    && isMoney(prices?.annual)
    && Number.isInteger(limits?.members)
    && Number.isInteger(limits?.channels)
    && Number.isInteger(limits?.scheduled_publications)
}

export function parsePublicCatalog(value: unknown): PublicCatalog {
  if (!value || typeof value !== 'object') {
    throw new Error('Il catalogo piani non è disponibile.')
  }

  const catalog = value as Record<string, unknown>
  if (
    catalog.provider !== 'stripe'
    || catalog.currency !== 'EUR'
    || !Array.isArray(catalog.plans)
    || catalog.plans.length !== 3
    || !catalog.plans.every(isPlan)
  ) {
    throw new Error('Il catalogo piani ricevuto non è valido.')
  }

  if (new Set(catalog.plans.map(plan => plan.code)).size !== 3) {
    throw new Error('Il catalogo piani contiene elementi duplicati.')
  }

  return catalog as unknown as PublicCatalog
}

export function formatMoney(money: Money): string {
  return new Intl.NumberFormat('it-IT', {
    style: 'currency',
    currency: money.currency,
    minimumFractionDigits: money.amount_cents % 100 === 0 ? 0 : 2,
  }).format(money.amount_cents / 100)
}

export function monthlyPrice(plan: PublicPlan, interval: BillingInterval): Money {
  if (interval === 'monthly') {
    return plan.prices.monthly
  }
  return {
    amount_cents: Math.round(plan.prices.annual.amount_cents / 12),
    currency: plan.prices.annual.currency,
  }
}
