import {
  priceForChannels,
  type BillingInterval,
  type Money,
  type PublicCatalog,
  type PublicPlan,
  type PublicPlanCode,
} from '../../f02-marketing-site/src/catalog.ts'
import type {
  BillingOverview,
  BillingUsage,
  DowngradeOverage,
  PlanChangeIntent,
  PlanChangeTarget,
  SubscriptionChangePreview,
} from './billing.ts'

const PLAN_RANK: Readonly<Record<PublicPlanCode, number>> = Object.freeze({
  start: 0,
  pro: 1,
  team: 2,
  unlimited: 3,
})

export interface PlanCompatibility {
  compatible: boolean
  overages: DowngradeOverage[]
}

export interface ProviderPreviewAmounts {
  currency: string
  immediate?: Money
  recurring?: Money
}

function usageFor(
  overview: BillingOverview,
  resource: BillingUsage['resource'],
): BillingUsage {
  const usage = overview.usage.find(candidate => candidate.resource === resource)
  if (!usage) {
    throw new Error('BILLING_USAGE_RESOURCE_MISSING')
  }
  return usage
}

function planLimit(
  plan: PublicPlan,
  resource: BillingUsage['resource'],
): number | null {
  if (resource === 'members') {
    return plan.limits.members
  }
  if (resource === 'channels') {
    return plan.limits.channels
  }
  return plan.limits.scheduled_publications
}

export function compatibilityForPlan(
  plan: PublicPlan,
  overview: BillingOverview,
): PlanCompatibility {
  const overages = overview.usage.flatMap((usage) => {
    const limit = planLimit(plan, usage.resource)
    if (limit === null || usage.used <= limit) {
      return []
    }
    return [{
      resource: usage.resource,
      used: usage.used,
      limit,
      excess: usage.used - limit,
    }]
  })
  return { compatible: overages.length === 0, overages }
}

export function minimumCompatiblePlan(
  catalog: PublicCatalog,
  overview: BillingOverview,
): PublicPlan {
  const ordered = [...catalog.plans].sort((left, right) =>
    PLAN_RANK[left.code] - PLAN_RANK[right.code])
  return ordered.find(plan => compatibilityForPlan(plan, overview).compatible)
    ?? ordered.at(-1)!
}

export function recommendedChannelQuantity(
  plan: PublicPlan,
  overview: BillingOverview,
): number | undefined {
  if (plan.code === 'start' || plan.limits.channels === null) {
    return undefined
  }
  const channels = usageFor(overview, 'channels')
  const currentCapacity = channels.limit ?? channels.used
  return Math.min(
    plan.limits.channels,
    Math.max(1, channels.used, currentCapacity),
  )
}

export function intentForPlan(
  plan: PublicPlan,
  interval: BillingInterval,
  overview: BillingOverview,
  channels = recommendedChannelQuantity(plan, overview),
): PlanChangeIntent {
  if (plan.code === 'start') {
    return { plan: 'start' }
  }
  if (plan.code === 'unlimited') {
    return { plan: 'unlimited', interval }
  }
  if (!channels
    || !Number.isSafeInteger(channels)
    || channels < 1
    || plan.limits.channels === null
    || channels > plan.limits.channels) {
    throw new Error('BILLING_INVALID_PLAN_CHANGE')
  }
  return { plan: plan.code, interval, channels }
}

export function priceForIntent(
  plan: PublicPlan,
  intent: PlanChangeIntent,
): Money {
  const interval = intent.interval ?? 'monthly'
  const channels = plan.code === 'start'
    ? plan.limits.channels
    : intent.channels ?? null
  return priceForChannels(plan, interval, channels)
}

export function currentPlanPrice(overview: BillingOverview): Money {
  const channelCapacity = usageFor(overview, 'channels').limit
  return priceForChannels(
    overview.plan,
    overview.interval,
    overview.plan.limits.channels === null
      ? null
      : channelCapacity ?? overview.plan.limits.channels,
  )
}

export function usagePercentage(usage: BillingUsage): number | null {
  if (usage.limit === null) {
    return null
  }
  if (usage.limit <= 0) {
    return usage.used > 0 ? 100 : 0
  }
  return Math.max(0, Math.round((usage.used / usage.limit) * 100))
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined
}

function totalFromTransaction(value: unknown): number | undefined {
  const transaction = record(value)
  const details = record(transaction?.details)
  const totals = record(details?.totals) ?? record(transaction?.totals)
  const raw = totals?.total
  if (typeof raw !== 'string' || !/^[0-9]+$/u.test(raw)) {
    return undefined
  }
  const amount = Number(raw)
  return Number.isSafeInteger(amount) ? amount : undefined
}

export function providerPreviewAmounts(
  preview: SubscriptionChangePreview,
): ProviderPreviewAmounts | undefined {
  const provider = record(preview.provider_preview)
  const data = record(provider?.data)
  const currency = data?.currency_code
  if (typeof currency !== 'string' || !/^[A-Z]{3}$/u.test(currency)) {
    return undefined
  }
  const immediate = totalFromTransaction(data?.immediate_transaction)
  const recurring = totalFromTransaction(data?.recurring_transaction_details)
  if (immediate === undefined && recurring === undefined) {
    return undefined
  }
  return {
    currency,
    immediate: immediate === undefined
      ? undefined
      : { amount_cents: immediate, currency: currency as 'EUR' },
    recurring: recurring === undefined
      ? undefined
      : { amount_cents: recurring, currency: currency as 'EUR' },
  }
}

export function overviewMatchesTarget(
  overview: BillingOverview,
  target: PlanChangeTarget,
): boolean {
  if (overview.plan.code !== target.plan || overview.interval !== target.interval) {
    return false
  }
  if (target.plan === 'pro' || target.plan === 'team') {
    return usageFor(overview, 'channels').limit === target.channels
  }
  return true
}

export function sameAsCurrentPlan(
  overview: BillingOverview,
  intent: PlanChangeIntent,
): boolean {
  if (overview.plan.code !== intent.plan) {
    return false
  }
  if (intent.plan === 'start') {
    return true
  }
  if (overview.interval !== intent.interval) {
    return false
  }
  if (intent.plan === 'pro' || intent.plan === 'team') {
    return usageFor(overview, 'channels').limit === intent.channels
  }
  return true
}
