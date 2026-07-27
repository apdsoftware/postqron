import type { PublicPlan } from './catalog'

export function quantityForPlan(plan: PublicPlan, selectedChannels: number): number | null {
  if (plan.limits.channels === null) {
    return null
  }

  return plan.purchasable
    ? Math.min(selectedChannels, plan.limits.channels)
    : plan.limits.channels
}

export function displayedChannelLimit(plan: PublicPlan): number | null {
  return plan.limits.channels
}
