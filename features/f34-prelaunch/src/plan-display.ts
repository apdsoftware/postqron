import type { PublicPlan } from '../../f02-marketing-site/src/catalog.ts'

export function pricingChannels(plan: PublicPlan): number | null {
  if (plan.limits.channels === null) {
    return null
  }

  return plan.purchasable ? Math.min(3, plan.limits.channels) : plan.limits.channels
}

export function planChannelLimit(plan: PublicPlan): number | null {
  return plan.limits.channels
}
