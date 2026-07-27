<script setup lang="ts">
import { computed } from '#imports'
import {
  formatMoney,
  interpolate,
  priceForChannels,
  pricingCopy,
  type PublicCatalog,
  type PublicPlan,
} from '../../f02-marketing-site/src/catalog.ts'
import { usePrelaunch } from '../runtime.ts'

const props = defineProps<{
  accessUrl: string
  catalog: PublicCatalog
}>()

const prelaunch = usePrelaunch()
const copy = computed(() => pricingCopy(prelaunch.locale.value))

function displayName(plan: PublicPlan): string {
  return plan.code === 'unlimited' ? copy.value.unlimitedName : plan.name
}

// A null channel limit is Unlimited's flat-priced, quota-free shape; there is
// no channel quantity to display or feed into the pricing calculation.
function displayedChannels(plan: PublicPlan): number | null {
  if (plan.limits.channels === null) {
    return null
  }
  return plan.purchasable ? Math.min(3, plan.limits.channels) : plan.limits.channels
}

function price(plan: PublicPlan): string {
  return formatMoney(
    priceForChannels(plan, 'monthly', displayedChannels(plan)),
    prelaunch.locale.value,
  )
}

function members(plan: PublicPlan): string {
  if (plan.limits.members === null) {
    return copy.value.unlimitedMembers
  }
  const key = plan.limits.members === 1 ? copy.value.member : copy.value.members
  return interpolate(key, { count: plan.limits.members })
}

function channels(plan: PublicPlan): string {
  const count = displayedChannels(plan)
  if (count === null) {
    return copy.value.unlimitedChannels
  }
  const key = count === 1 ? copy.value.channel : copy.value.channels
  return interpolate(key, { count })
}

function scheduled(plan: PublicPlan): string {
  if (plan.limits.scheduled_publications_per_channel === null) {
    return copy.value.unlimitedScheduled
  }
  return interpolate(copy.value.scheduledPerChannel, {
    count: plan.limits.scheduled_publications_per_channel,
  })
}
</script>

<template>
  <section
    class="prelaunch-pricing"
    aria-labelledby="prelaunch-pricing-title"
  >
    <header class="prelaunch-pricing__heading">
      <p class="eyebrow">
        {{ prelaunch.translate('pricing.eyebrow') }}
      </p>
      <h2 id="prelaunch-pricing-title">
        {{ prelaunch.translate('pricing.title') }}
      </h2>
      <p>{{ prelaunch.translate('pricing.description') }}</p>
    </header>

    <div class="prelaunch-pricing__grid">
      <article
        v-for="plan in props.catalog.plans"
        :key="plan.code"
        class="prelaunch-plan"
        :class="{ 'prelaunch-plan--featured': plan.code === 'pro' }"
      >
        <span
          v-if="plan.code === 'pro'"
          class="prelaunch-plan__badge"
        >
          {{ copy.featured }}
        </span>
        <p class="prelaunch-plan__name">
          {{ displayName(plan) }}
        </p>
        <p class="prelaunch-plan__price">
          <strong>{{ price(plan) }}</strong>
          <span>{{ copy.perMonth }}</span>
        </p>
        <p class="prelaunch-plan__billing">
          {{ plan.purchasable ? copy.monthlyBilling : copy.freeForever }}
        </p>
        <ul>
          <li>{{ members(plan) }}</li>
          <li>{{ channels(plan) }}</li>
          <li>{{ scheduled(plan) }}</li>
        </ul>
        <NuxtLink
          class="pq-button"
          :class="{ 'pq-button--secondary': plan.code !== 'pro' }"
          :to="props.accessUrl"
          :prefetch="false"
        >
          {{ prelaunch.translate('pricing.cta') }}
        </NuxtLink>
      </article>
    </div>

    <p class="prelaunch-pricing__note">
      {{ prelaunch.translate('pricing.note') }}
    </p>
  </section>
</template>

<style scoped>
.prelaunch-pricing {
  margin-top: clamp(4rem, 10vw, 8rem);
}

.prelaunch-pricing__heading {
  max-width: 48rem;
  margin-inline: auto;
  text-align: center;
}

.prelaunch-pricing__heading h2 {
  margin: 0;
  font-size: clamp(2rem, 5vw, 3.4rem);
  line-height: var(--pq-line-height-tight);
  letter-spacing: var(--pq-letter-spacing-tight);
}

.prelaunch-pricing__heading > p:last-child {
  max-width: 62ch;
  margin: 1rem auto 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-lg);
}

.prelaunch-pricing__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1.25rem;
  align-items: stretch;
  margin-top: 2.5rem;
}

.prelaunch-plan {
  position: relative;
  display: flex;
  min-width: 0;
  flex-direction: column;
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-xl);
  padding: clamp(1.35rem, 3vw, 2rem);
  background: #fff;
  box-shadow: var(--pq-shadow-sm);
}

.prelaunch-plan--featured {
  border-color: var(--pq-color-brand);
  box-shadow: 0 1.25rem 3rem #185c431f;
}

.prelaunch-plan__badge {
  width: fit-content;
  margin: -0.25rem 0 1rem;
  border-radius: 999px;
  padding: 0.35rem 0.7rem;
  color: var(--pq-color-text-inverse);
  background: var(--pq-color-brand);
  font-size: var(--pq-font-size-xs);
  font-weight: var(--pq-font-weight-bold);
}

.prelaunch-plan__name {
  margin: 0;
  color: var(--pq-color-brand);
  font-weight: var(--pq-font-weight-bold);
  letter-spacing: var(--pq-letter-spacing-wide);
  text-transform: uppercase;
}

.prelaunch-plan__price {
  display: flex;
  gap: 0.45rem;
  align-items: baseline;
  margin: 1.25rem 0 0;
}

.prelaunch-plan__price strong {
  font-size: clamp(2.1rem, 4vw, 3.25rem);
  line-height: 1;
  letter-spacing: var(--pq-letter-spacing-tight);
}

.prelaunch-plan__price span,
.prelaunch-plan__billing,
.prelaunch-pricing__note {
  color: var(--pq-color-text-muted);
}

.prelaunch-plan__billing {
  min-height: 2.75rem;
  margin: 0.65rem 0 0;
  font-size: var(--pq-font-size-sm);
}

.prelaunch-plan ul {
  display: grid;
  gap: 0.75rem;
  margin: 1.5rem 0;
  padding: 0;
  list-style: none;
}

.prelaunch-plan li {
  position: relative;
  padding-left: 1.5rem;
  color: var(--pq-color-text-muted);
}

.prelaunch-plan li::before {
  position: absolute;
  left: 0;
  color: var(--pq-color-brand);
  content: "✓";
  font-weight: var(--pq-font-weight-bold);
}

.prelaunch-plan .pq-button {
  width: 100%;
  margin-top: auto;
  text-align: center;
}

.prelaunch-pricing__note {
  max-width: 60rem;
  margin: 1.5rem auto 0;
  font-size: var(--pq-font-size-sm);
  text-align: center;
}

@media (max-width: 64rem) {
  .prelaunch-pricing__grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 52rem) {
  .prelaunch-pricing__grid {
    grid-template-columns: 1fr;
    max-width: 34rem;
    margin-inline: auto;
  }
}

@media (max-width: 24rem) {
  .prelaunch-plan__price {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
