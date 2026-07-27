<script setup lang="ts">
import { computed, ref } from '#imports'
import {
  formatMoney,
  interpolate,
  monthlyEquivalent,
  pricingCopy,
  type BillingInterval,
  type PublicCatalog,
  type PublicPlan,
} from '../../f02-marketing-site/src/catalog.ts'
import {
  OVER_MAX_QUANTITY,
  annualBillingTerms,
  annualSaving,
  billedChannels,
  formatPercent,
  initialPricingSelection,
  isPlanCompatible,
  orderedPlans,
  overMaxThreshold,
  perChannelPrice,
  planTotal,
  selectedPlan,
  withInterval,
  withPlan,
  withQuantity,
  type ChannelQuantity,
} from '../../f02-marketing-site/src/pricing-model.ts'
import { usePrelaunch } from '../runtime.ts'

const props = defineProps<{
  accessUrl: string
  catalog: PublicCatalog
}>()

const prelaunch = usePrelaunch()
const copy = computed(() => pricingCopy(prelaunch.locale.value))
const selection = ref(initialPricingSelection())
const plans = computed(() => orderedPlans(props.catalog))
const currentPlan = computed(() => selectedPlan(props.catalog, selection.value))
const sliderMaximum = computed(() => overMaxThreshold(props.catalog))
const thresholdMarkers = computed(() => [1, 3, 6, 9, sliderMaximum.value])
const annualTerms = computed(() => annualBillingTerms(props.catalog))
const annualTermsParams = computed(() => ({
  months: number(annualTerms.value.monthsCharged),
  serviceMonths: number(annualTerms.value.monthsOfService),
  percent: formatPercent(annualTerms.value.savingRatio, prelaunch.locale.value),
}))
const selectionAnnouncement = computed(() =>
  interpolate(copy.value.selectedPlanAnnouncement, {
    plan: displayName(currentPlan.value),
    quantity: quantityOptionLabel(selection.value.quantity),
    interval: selection.value.interval === 'monthly'
      ? copy.value.monthly
      : copy.value.annual,
    total: `${formatMoney(total(currentPlan.value), prelaunch.locale.value)}${
      selection.value.interval === 'monthly'
        ? copy.value.perMonth
        : copy.value.perYear
    }`,
  }))

function number(value: number): string {
  return new Intl.NumberFormat(prelaunch.locale.value).format(value)
}

function displayName(plan: PublicPlan): string {
  return plan.code === 'unlimited' ? copy.value.unlimitedName : plan.name
}

function quantityOptionLabel(option: ChannelQuantity): string {
  return option === OVER_MAX_QUANTITY
    ? `${number(sliderMaximum.value)}+`
    : number(option)
}

function quantityValueText(option: ChannelQuantity): string {
  const label = quantityOptionLabel(option)
  const key = option === 1 ? copy.value.channel : copy.value.channels
  return interpolate(key, { count: label })
}

function setBillingInterval(interval: BillingInterval) {
  selection.value = withInterval(selection.value, interval)
}

const sliderPosition = computed({
  get: () => selection.value.quantity === OVER_MAX_QUANTITY
    ? sliderMaximum.value
    : selection.value.quantity,
  set: (position: number) => {
    const quantity: ChannelQuantity = position === sliderMaximum.value
      ? OVER_MAX_QUANTITY
      : position
    selection.value = withQuantity(props.catalog, selection.value, quantity)
  },
})

function compatible(plan: PublicPlan): boolean {
  return isPlanCompatible(plan, selection.value.quantity)
}

function isSelected(plan: PublicPlan): boolean {
  return currentPlan.value.code === plan.code
}

function choose(plan: PublicPlan) {
  if (compatible(plan)) {
    selection.value = withPlan(props.catalog, selection.value, plan.code)
  }
}

function total(plan: PublicPlan) {
  return planTotal(plan, selection.value.interval, selection.value.quantity)
}

function totalForChannelsLine(plan: PublicPlan): string | null {
  if (!plan.purchasable) {
    return null
  }
  const channels = billedChannels(plan, selection.value.quantity)
  if (channels === null) {
    return null
  }
  return channels === 1
    ? copy.value.totalForChannel
    : interpolate(copy.value.totalForChannels, { count: number(channels) })
}

function perChannelLine(plan: PublicPlan): string | null {
  const unit = perChannelPrice(
    plan,
    selection.value.interval,
    selection.value.quantity,
  )
  if (unit === null) {
    return null
  }
  const key = selection.value.interval === 'monthly'
    ? copy.value.perChannelMonthly
    : copy.value.perChannelAnnual
  return interpolate(key, {
    amount: formatMoney(unit, prelaunch.locale.value),
  })
}

function annualSavingLine(plan: PublicPlan): string {
  return interpolate(copy.value.annualSavingAmount, {
    amount: formatMoney(
      annualSaving(plan, selection.value.quantity),
      prelaunch.locale.value,
    ),
  })
}

function membersLine(plan: PublicPlan): string {
  if (plan.limits.members === null) {
    return copy.value.usersIncludedUnlimited
  }
  if (plan.limits.members === 1) {
    return copy.value.usersIncludedOne
  }
  return interpolate(copy.value.usersIncludedMany, {
    count: number(plan.limits.members),
  })
}

function channelsLine(plan: PublicPlan): string {
  if (plan.limits.channels === null) {
    return copy.value.unlimitedChannels
  }
  const key = plan.limits.channels === 1 ? copy.value.channel : copy.value.channels
  return interpolate(key, { count: number(plan.limits.channels) })
}

function scheduledLine(plan: PublicPlan): string {
  if (plan.limits.scheduled_publications_per_channel === null) {
    return copy.value.unlimitedScheduled
  }
  return interpolate(copy.value.scheduledPerChannel, {
    count: number(plan.limits.scheduled_publications_per_channel),
  })
}

function incompatibleReason(plan: PublicPlan): string {
  if (plan.limits.channels === null) {
    throw new Error('PRELAUNCH_PRICING_UNEXPECTED_COMPATIBILITY')
  }
  return interpolate(copy.value.incompatibleChannels, {
    plan: displayName(plan),
    count: number(plan.limits.channels),
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

    <div class="prelaunch-pricing__controls">
      <div
        class="prelaunch-pricing__interval"
        role="group"
        :aria-label="copy.intervalLabel"
      >
        <button
          type="button"
          :aria-pressed="selection.interval === 'monthly'"
          @click="setBillingInterval('monthly')"
        >
          {{ copy.monthly }}
        </button>
        <button
          type="button"
          :aria-pressed="selection.interval === 'annual'"
          @click="setBillingInterval('annual')"
        >
          {{ interpolate(copy.annualOption, annualTermsParams) }}
        </button>
      </div>

      <p class="prelaunch-pricing__annual-explainer">
        {{ interpolate(copy.annualExplainer, annualTermsParams) }}
      </p>

      <div class="prelaunch-pricing__quantity">
        <div class="prelaunch-pricing__quantity-heading">
          <label for="prelaunch-channel-quantity">
            <strong>{{ copy.quantityLabel }}</strong>
          </label>
          <output
            for="prelaunch-channel-quantity"
            aria-live="polite"
          >
            {{ quantityValueText(selection.quantity) }}
          </output>
        </div>
        <input
          id="prelaunch-channel-quantity"
          v-model.number="sliderPosition"
          type="range"
          min="1"
          :max="sliderMaximum"
          step="1"
          :aria-valuetext="quantityValueText(selection.quantity)"
        >
        <div
          class="prelaunch-pricing__markers"
          aria-hidden="true"
        >
          <span
            v-for="marker in thresholdMarkers"
            :key="marker"
          >
            {{ marker === sliderMaximum ? `${number(marker)}+` : number(marker) }}
          </span>
        </div>
        <p class="prelaunch-pricing__guide">
          {{ prelaunch.translate('pricing.sliderGuide') }}
        </p>
        <small>{{ copy.quantityHelp }}</small>
      </div>
    </div>

    <p
      class="sr-only"
      aria-live="polite"
    >
      {{ selectionAnnouncement }}
    </p>

    <div
      class="prelaunch-pricing__grid"
      role="radiogroup"
      :aria-label="copy.planGroupLabel"
    >
      <article
        v-for="plan in plans"
        :key="plan.code"
        :data-plan="plan.code"
        class="prelaunch-plan"
        :class="{
          'prelaunch-plan--featured': plan.code === 'pro',
          'prelaunch-plan--selected': isSelected(plan),
          'prelaunch-plan--disabled': !compatible(plan),
        }"
      >
        <input
          :id="`prelaunch-plan-choice-${plan.code}`"
          class="prelaunch-plan__input"
          type="radio"
          name="prelaunch-plan-choice"
          :value="plan.code"
          :checked="isSelected(plan)"
          :disabled="!compatible(plan)"
          :aria-label="displayName(plan)"
          :aria-describedby="compatible(plan)
            ? undefined
            : `prelaunch-plan-incompatible-${plan.code}`"
          @change="choose(plan)"
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

        <template v-if="compatible(plan)">
          <p class="prelaunch-plan__price">
            <strong>
              {{ formatMoney(total(plan), prelaunch.locale.value) }}
            </strong>
            <span>
              {{ selection.interval === 'monthly'
                ? copy.perMonth
                : copy.perYear }}
            </span>
          </p>
          <div class="prelaunch-plan__billing">
            <span v-if="totalForChannelsLine(plan)">
              {{ totalForChannelsLine(plan) }}
            </span>
            <span v-if="perChannelLine(plan)">
              {{ perChannelLine(plan) }}
            </span>
            <template v-if="!plan.purchasable">
              <span>{{ copy.freeForever }}</span>
              <span>{{ copy.startSelectorNote }}</span>
            </template>
            <template v-else-if="selection.interval === 'annual'">
              <span>
                {{ interpolate(copy.annualBilling, {
                  total: formatMoney(total(plan), prelaunch.locale.value),
                }) }}
              </span>
              <span>
                {{ interpolate(copy.annualEquivalent, {
                  monthly: formatMoney(
                    monthlyEquivalent(total(plan), selection.interval),
                    prelaunch.locale.value,
                  ),
                }) }}
              </span>
              <span>
                {{ interpolate(copy.annualPayForService, annualTermsParams) }}
              </span>
              <span>{{ annualSavingLine(plan) }}</span>
            </template>
            <span v-else>{{ copy.monthlyBilling }}</span>
            <span v-if="plan.limits.channels === null">
              {{ copy.unlimitedFlatIndependent }}
            </span>
          </div>
        </template>
        <p
          v-else
          :id="`prelaunch-plan-incompatible-${plan.code}`"
          class="prelaunch-plan__incompatible"
        >
          {{ incompatibleReason(plan) }}
        </p>

        <ul>
          <li>{{ membersLine(plan) }}</li>
          <li>{{ channelsLine(plan) }}</li>
          <li>{{ scheduledLine(plan) }}</li>
        </ul>
        <NuxtLink
          v-if="compatible(plan)"
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

.prelaunch-pricing__controls {
  display: grid;
  justify-items: center;
  gap: 1rem;
  margin-top: 2rem;
}

.prelaunch-pricing__interval {
  display: inline-flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 0.4rem;
  border: 1px solid var(--pq-color-border);
  border-radius: 999px;
  padding: 0.35rem;
  background: #fff;
}

.prelaunch-pricing__interval button {
  min-height: var(--pq-size-target-min);
  border: 0;
  border-radius: 999px;
  padding: 0.65rem 1rem;
  color: var(--pq-color-text);
  background: transparent;
  font: inherit;
  font-weight: var(--pq-font-weight-semibold);
  cursor: pointer;
}

.prelaunch-pricing__interval button[aria-pressed="true"] {
  color: var(--pq-color-text-inverse);
  background: var(--pq-color-brand);
}

.prelaunch-pricing__annual-explainer {
  max-width: 44rem;
  margin: 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
  text-align: center;
}

.prelaunch-pricing__quantity {
  display: grid;
  width: min(38rem, 100%);
  gap: 0.65rem;
  color: var(--pq-color-text);
}

.prelaunch-pricing__quantity-heading {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 0.5rem 1rem;
  align-items: baseline;
}

.prelaunch-pricing__quantity output {
  color: var(--pq-color-brand);
  font-weight: var(--pq-font-weight-bold);
}

.prelaunch-pricing__quantity input[type="range"] {
  width: 100%;
  min-height: var(--pq-size-target-min);
  margin: 0;
  accent-color: var(--pq-color-brand);
  cursor: pointer;
  touch-action: manipulation;
}

.prelaunch-pricing__quantity input[type="range"]:focus-visible {
  border-radius: var(--pq-radius-md);
  outline: 3px solid var(--pq-color-brand);
  outline-offset: 2px;
}

.prelaunch-pricing__markers {
  display: flex;
  justify-content: space-between;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-xs);
  font-variant-numeric: tabular-nums;
}

.prelaunch-pricing__guide {
  margin: 0;
  color: var(--pq-color-text);
  font-size: var(--pq-font-size-sm);
  font-weight: var(--pq-font-weight-semibold);
  text-align: center;
}

.prelaunch-pricing__quantity small {
  color: var(--pq-color-text-muted);
}

.sr-only {
  position: absolute;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  border: 0;
  white-space: nowrap;
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

.prelaunch-plan--selected {
  border-color: var(--pq-color-brand);
  box-shadow: 0 1.25rem 3rem #185c4329;
}

.prelaunch-plan--disabled {
  border-style: dashed;
  background: var(--pq-color-surface-subtle);
  box-shadow: none;
}

.prelaunch-plan:focus-within {
  outline: 3px solid var(--pq-color-brand);
  outline-offset: 2px;
}

.prelaunch-plan__input {
  position: absolute;
  inset: 0;
  z-index: 1;
  width: 100%;
  height: 100%;
  margin: 0;
  opacity: 0;
  cursor: pointer;
}

.prelaunch-plan__input:disabled {
  cursor: not-allowed;
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
  display: grid;
  min-height: 2.75rem;
  gap: 0.35rem;
  margin: 0.65rem 0 0;
  font-size: var(--pq-font-size-sm);
}

.prelaunch-plan__incompatible {
  margin: 1.25rem 0 0;
  color: var(--pq-color-text-muted);
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
  position: relative;
  z-index: 2;
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
