<script setup lang="ts">
import {
  computed,
  ref,
  useRoute,
  useRuntimeConfig,
} from '#imports'
import {
  bufferBenchmark,
  formatMoney,
  interpolate,
  localeFromPath,
  monthlyEquivalent,
  pricingCopy,
  purchaseHref,
  savingsAgainstBuffer,
  type BillingInterval,
  type PublicCatalog,
  type PublicPlan,
} from '~/src/catalog'
import { displayedChannelLimit } from '~/src/plan-display'
import {
  OVER_MAX_QUANTITY,
  annualBillingTerms,
  annualSaving,
  billedChannels,
  checkoutIntentFor,
  formatPercent,
  initialPricingSelection,
  isPlanCompatible,
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
} from '~/src/pricing-model'

const props = defineProps<{
  catalog: PublicCatalog
}>()
const config = useRuntimeConfig()
const route = useRoute()
const selection = ref(initialPricingSelection())
const locale = computed(() => localeFromPath(route.fullPath))
const copy = computed(() => pricingCopy(locale.value))
const plans = computed(() => orderedPlans(props.catalog))
const paidPlans = computed(() =>
  props.catalog.plans.filter((plan): plan is PublicPlan & { code: 'pro' | 'team' } =>
    plan.code === 'pro' || plan.code === 'team'))
const currentPlan = computed(() => selectedPlan(props.catalog, selection.value))
const channelOptions = computed(() => quantityOptions(props.catalog))
const annualTerms = computed(() => annualBillingTerms(props.catalog))
const annualTermsParams = computed(() => ({
  months: number(annualTerms.value.monthsCharged),
  serviceMonths: number(annualTerms.value.monthsOfService),
  percent: formatPercent(annualTerms.value.savingRatio, locale.value),
}))
const benchmarkChannels = computed(() =>
  selection.value.quantity === OVER_MAX_QUANTITY ? null : selection.value.quantity)
const selectionAnnouncement = computed(() => interpolate(copy.value.selectedPlanAnnouncement, {
  plan: displayName(currentPlan.value),
  quantity: quantityOptionLabel(selection.value.quantity),
  interval: selection.value.interval === 'monthly' ? copy.value.monthly : copy.value.annual,
  total: `${formatMoney(total(currentPlan.value), locale.value)}${
    selection.value.interval === 'monthly' ? copy.value.perMonth : copy.value.perYear}`,
}))
const benchmarkPlans = computed(() =>
  benchmarkChannels.value === null
    ? []
    : paidPlans.value.filter(plan => isPlanCompatible(plan, benchmarkChannels.value as number)))

function number(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function displayName(plan: PublicPlan): string {
  return plan.code === 'unlimited' ? copy.value.unlimitedName : plan.name
}

function quantityOptionLabel(option: ChannelQuantity): string {
  return option === OVER_MAX_QUANTITY
    ? interpolate(copy.value.quantityOverMax, { count: number(overMaxThreshold(props.catalog)) })
    : number(option)
}

function setBillingInterval(interval: BillingInterval) {
  selection.value = withInterval(selection.value, interval)
}

const quantityValue = computed({
  get: () => String(selection.value.quantity),
  set: (raw: string) => {
    const quantity: ChannelQuantity = raw === OVER_MAX_QUANTITY
      ? OVER_MAX_QUANTITY
      : Number(raw)
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
  const unit = perChannelPrice(plan, selection.value.interval, selection.value.quantity)
  if (unit === null) {
    return null
  }
  const key = selection.value.interval === 'monthly'
    ? copy.value.perChannelMonthly
    : copy.value.perChannelAnnual
  return interpolate(key, { amount: formatMoney(unit, locale.value) })
}

function annualSavingLine(plan: PublicPlan): string {
  return interpolate(copy.value.annualSavingAmount, {
    amount: formatMoney(annualSaving(plan, selection.value.quantity), locale.value),
  })
}

function membersLine(plan: PublicPlan): string {
  if (plan.limits.members === null) {
    return copy.value.usersIncludedUnlimited
  }
  if (plan.limits.members === 1) {
    return copy.value.usersIncludedOne
  }
  return interpolate(copy.value.usersIncludedMany, { count: number(plan.limits.members) })
}

function channelLimit(plan: PublicPlan): string {
  const count = displayedChannelLimit(plan)
  if (count === null) {
    return copy.value.unlimitedChannels
  }
  const key = count === 1 ? copy.value.channel : copy.value.channels
  return interpolate(key, { count: number(count) })
}

function scheduledLimit(plan: PublicPlan): string {
  if (plan.limits.scheduled_publications_per_channel === null) {
    return copy.value.unlimitedScheduled
  }
  return interpolate(copy.value.scheduledPerChannel, {
    count: number(plan.limits.scheduled_publications_per_channel),
  })
}

function incompatibleReason(plan: PublicPlan): string {
  return interpolate(copy.value.incompatibleChannels, {
    plan: displayName(plan),
    count: number(plan.limits.channels ?? 0),
  })
}

function cta(plan: PublicPlan): string {
  return plan.purchasable
    ? interpolate(copy.value.choosePlan, { plan: displayName(plan) })
    : copy.value.chooseFree
}

function href(plan: PublicPlan): string {
  const intent = checkoutIntentFor(plan, selection.value.interval, selection.value.quantity)
  const quantityPart = intent.quantity === null ? '' : `&quantity=${intent.quantity}`
  const runtimeIntent = `${config.public.appUrl}?plan=${intent.plan}&interval=${intent.interval}${quantityPart}`
  return purchaseHref(
    runtimeIntent,
    locale.value,
    plan,
    intent.interval,
    intent.quantity,
  )
}
</script>

<template>
  <div>
    <div class="pricing-controls">
      <div
        class="billing-toggle"
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

      <p class="annual-explainer">
        {{ interpolate(copy.annualExplainer, annualTermsParams) }}
      </p>

      <label class="quantity-control">
        <span>
          <strong>{{ copy.quantityLabel }}</strong>
        </span>
        <select
          id="pricing-channel-quantity"
          v-model="quantityValue"
        >
          <option
            v-for="option in channelOptions"
            :key="String(option)"
            :value="String(option)"
          >
            {{ quantityOptionLabel(option) }}
          </option>
        </select>
        <small>{{ copy.quantityHelp }}</small>
      </label>
    </div>

    <p
      class="sr-only"
      aria-live="polite"
    >
      {{ selectionAnnouncement }}
    </p>

    <div
      class="pricing-grid"
      role="radiogroup"
      :aria-label="copy.planGroupLabel"
    >
      <article
        v-for="plan in plans"
        :key="plan.code"
        class="plan-card"
        :class="{
          'plan-card--featured': plan.code === 'pro',
          'plan-card--selected': isSelected(plan),
          'plan-card--disabled': !compatible(plan),
        }"
      >
        <input
          :id="`plan-choice-${plan.code}`"
          class="plan-card__input"
          type="radio"
          name="public-plan-choice"
          :value="plan.code"
          :checked="isSelected(plan)"
          :disabled="!compatible(plan)"
          :aria-label="displayName(plan)"
          :aria-describedby="compatible(plan) ? undefined : `plan-incompatible-${plan.code}`"
          @change="choose(plan)"
        >
        <span
          v-if="plan.code === 'pro'"
          class="plan-card__badge"
        >{{ copy.featured }}</span>
        <p class="plan-card__name">
          {{ displayName(plan) }}
        </p>
        <template v-if="compatible(plan)">
          <p class="plan-card__price">
            <strong>{{ formatMoney(total(plan), locale) }}</strong>
            <span>{{ selection.interval === 'monthly' ? copy.perMonth : copy.perYear }}</span>
          </p>
          <div class="plan-card__billing">
            <span v-if="totalForChannelsLine(plan)">{{ totalForChannelsLine(plan) }}</span>
            <span v-if="perChannelLine(plan)">{{ perChannelLine(plan) }}</span>
            <template v-if="!plan.purchasable">
              <span>{{ copy.freeForever }}</span>
              <span>{{ copy.startSelectorNote }}</span>
            </template>
            <template v-else-if="selection.interval === 'annual'">
              <span>
                {{ interpolate(copy.annualBilling, {
                  total: formatMoney(total(plan), locale),
                }) }}
              </span>
              <span>
                {{ interpolate(copy.annualEquivalent, {
                  monthly: formatMoney(monthlyEquivalent(total(plan), selection.interval), locale),
                }) }}
              </span>
              <span>{{ interpolate(copy.annualPayForService, annualTermsParams) }}</span>
              <span>{{ annualSavingLine(plan) }}</span>
            </template>
            <template v-else>
              <span>{{ copy.monthlyBilling }}</span>
            </template>
            <span v-if="plan.limits.channels === null">{{ copy.unlimitedFlatIndependent }}</span>
          </div>
          <a
            class="pq-button"
            :class="{ 'pq-button--secondary': plan.code !== 'pro' }"
            :href="href(plan)"
          >
            {{ cta(plan) }}
          </a>
        </template>
        <template v-else>
          <p
            :id="`plan-incompatible-${plan.code}`"
            class="plan-card__incompatible"
          >
            {{ incompatibleReason(plan) }}
          </p>
        </template>
        <ul>
          <li>{{ membersLine(plan) }}</li>
          <li>{{ channelLimit(plan) }}</li>
          <li>{{ scheduledLimit(plan) }}</li>
          <li v-if="plan.trial">
            {{ copy.trial }}
          </li>
        </ul>
      </article>
    </div>

    <p class="tax-notice">
      {{ copy.taxNotice }}
    </p>

    <section
      v-if="benchmarkPlans.length > 0"
      class="benchmark"
      aria-labelledby="buffer-benchmark-title"
    >
      <h2 id="buffer-benchmark-title">
        {{ copy.benchmarkTitle }}
      </h2>
      <p>{{ copy.benchmarkIntro }}</p>
      <div class="benchmark__table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">
                {{ copy.benchmarkPlan }}
              </th>
              <th scope="col">
                {{ copy.postqron }}
              </th>
              <th scope="col">
                {{ copy.buffer }}
              </th>
              <th scope="col">
                {{ copy.saving.replace(' {amount}', '') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="plan in benchmarkPlans"
              :key="plan.code"
            >
              <th scope="row">
                {{ plan.name }} /
                {{ plan.code === 'pro' ? 'Buffer Essentials' : 'Buffer Team' }}
              </th>
              <td>{{ formatMoney(total(plan), locale) }}</td>
              <td>
                {{ formatMoney(
                  bufferBenchmark(plan.code, selection.interval, benchmarkChannels as number),
                  locale,
                ) }}
              </td>
              <td>
                {{ interpolate(copy.saving, {
                  amount: formatMoney(
                    savingsAgainstBuffer(plan, selection.interval, benchmarkChannels as number),
                    locale,
                  ),
                }) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p>{{ copy.comparisonScope }}</p>
      <p class="benchmark__limits">
        {{ copy.comparisonLimits }}
      </p>
    </section>
  </div>
</template>

<style scoped>
.pricing-controls {
  display: grid;
  gap: var(--pq-space-6);
  justify-items: center;
  margin-bottom: var(--pq-space-10);
}

.billing-toggle {
  margin-bottom: 0;
}

.annual-explainer {
  max-width: 42rem;
  margin: 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
  text-align: center;
}

.quantity-control {
  display: grid;
  width: min(30rem, 100%);
  gap: var(--pq-space-2);
  color: var(--pq-color-text-muted);
}

.quantity-control > span {
  color: var(--pq-color-text);
}

.quantity-control select {
  width: 100%;
  min-height: var(--pq-size-target-min);
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-md);
  padding: var(--pq-space-2) var(--pq-space-3);
  color: var(--pq-color-text);
  background: var(--pq-color-surface);
  font-size: var(--pq-font-size-md);
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

.plan-card__input {
  position: absolute;
  inset: 0;
  z-index: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  opacity: 0;
  cursor: pointer;
}

.plan-card__input:disabled {
  cursor: not-allowed;
}

.plan-card--selected {
  border-color: var(--pq-color-brand);
  box-shadow: var(--pq-shadow-md);
}

.plan-card--disabled {
  opacity: 0.55;
}

.plan-card:focus-within {
  outline: 3px solid var(--pq-color-brand);
  outline-offset: 2px;
}

.plan-card .pq-button {
  position: relative;
  z-index: 1;
}

.plan-card__incompatible {
  margin: var(--pq-space-5) 0 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
}

.plan-card__billing {
  display: grid;
  gap: var(--pq-space-1);
}

.tax-notice {
  max-width: 62rem;
  margin: var(--pq-space-8) auto 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
  text-align: center;
}

.benchmark {
  margin-top: clamp(4rem, 8vw, 7rem);
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-xl);
  padding: clamp(1.5rem, 5vw, 3rem);
  background: var(--pq-color-surface);
}

.benchmark h2 {
  margin-top: 0;
  font-size: var(--pq-font-size-2xl);
}

.benchmark > p {
  color: var(--pq-color-text-muted);
}

.benchmark__table-wrap {
  overflow-x: auto;
  margin-block: var(--pq-space-6);
}

.benchmark table {
  width: 100%;
  min-width: 42rem;
  border-collapse: collapse;
}

.benchmark th,
.benchmark td {
  border-bottom: 1px solid var(--pq-color-border);
  padding: var(--pq-space-4);
  text-align: left;
}

.benchmark thead th {
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
}

.benchmark__limits {
  border-left: 4px solid var(--pq-color-accent);
  padding-left: var(--pq-space-4);
}
</style>
