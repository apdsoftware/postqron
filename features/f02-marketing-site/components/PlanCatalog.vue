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
  priceForChannels,
  pricingCopy,
  purchaseHref,
  savingsAgainstBuffer,
  type BillingInterval,
  type PublicCatalog,
  type PublicPlan,
} from '~/src/catalog'

const props = defineProps<{
  catalog: PublicCatalog
}>()
const config = useRuntimeConfig()
const route = useRoute()
const interval = ref<BillingInterval>('monthly')
const channels = ref(3)
const locale = computed(() => localeFromPath(route.fullPath))
const copy = computed(() => pricingCopy(locale.value))
const paidPlans = computed(() =>
  props.catalog.plans.filter((plan): plan is PublicPlan & { code: 'pro' | 'team' } =>
    plan.code === 'pro' || plan.code === 'team'))

function number(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value)
}

function total(plan: PublicPlan) {
  return priceForChannels(
    plan,
    interval.value,
    plan.purchasable ? channels.value : plan.limits.channels,
  )
}

function members(plan: PublicPlan): string {
  const key = plan.limits.members === 1 ? copy.value.member : copy.value.members
  return interpolate(key, { count: number(plan.limits.members) })
}

function channelLimit(plan: PublicPlan): string {
  const count = plan.purchasable ? channels.value : plan.limits.channels
  const key = count === 1 ? copy.value.channel : copy.value.channels
  return interpolate(key, { count: number(count) })
}

function scheduledLimit(plan: PublicPlan): string {
  return interpolate(copy.value.scheduledPerChannel, {
    count: number(plan.limits.scheduled_publications_per_channel),
  })
}

function cta(plan: PublicPlan): string {
  return plan.purchasable
    ? interpolate(copy.value.choosePlan, { plan: plan.name })
    : copy.value.chooseFree
}

function href(plan: PublicPlan): string {
  const quantity = plan.purchasable ? channels.value : plan.limits.channels
  const runtimeIntent = `${config.public.appUrl}?plan=${plan.code}&interval=${interval.value}&quantity=${quantity}`
  return purchaseHref(
    runtimeIntent,
    locale.value,
    plan,
    interval.value,
    channels.value,
  )
}
</script>

<template>
  <div>
    <div class="pricing-controls">
      <div
        class="billing-toggle"
        :aria-label="copy.intervalLabel"
      >
        <button
          type="button"
          :aria-pressed="interval === 'monthly'"
          @click="interval = 'monthly'"
        >
          {{ copy.monthly }}
        </button>
        <button
          type="button"
          :aria-pressed="interval === 'annual'"
          @click="interval = 'annual'"
        >
          {{ copy.annual }}
          <span>{{ copy.annualBadge }}</span>
        </button>
      </div>

      <label class="quantity-control">
        <span>
          <strong>{{ copy.quantityLabel }}</strong>
          <output for="pricing-channel-quantity">{{ number(channels) }}</output>
        </span>
        <input
          id="pricing-channel-quantity"
          v-model.number="channels"
          type="range"
          min="1"
          max="50"
          step="1"
        >
        <small>{{ copy.quantityHelp }}</small>
      </label>
    </div>

    <div class="pricing-grid">
      <article
        v-for="plan in props.catalog.plans"
        :key="plan.code"
        class="plan-card"
        :class="{ 'plan-card--featured': plan.code === 'pro' }"
      >
        <span
          v-if="plan.code === 'pro'"
          class="plan-card__badge"
        >{{ copy.featured }}</span>
        <p class="plan-card__name">
          {{ plan.name }}
        </p>
        <p class="plan-card__price">
          <strong>{{ formatMoney(total(plan), locale) }}</strong>
          <span>{{ interval === 'monthly' ? copy.perMonth : copy.perYear }}</span>
        </p>
        <div class="plan-card__billing">
          <template v-if="!plan.purchasable">
            {{ copy.freeForever }}
          </template>
          <template v-else-if="interval === 'annual'">
            <span>
              {{ interpolate(copy.annualBilling, {
                total: formatMoney(total(plan), locale),
              }) }}
            </span>
            <span>
              {{ interpolate(copy.annualEquivalent, {
                monthly: formatMoney(monthlyEquivalent(total(plan), interval), locale),
              }) }}
            </span>
          </template>
          <template v-else>
            {{ copy.monthlyBilling }}
          </template>
        </div>
        <a
          class="pq-button"
          :class="{ 'pq-button--secondary': plan.code !== 'pro' }"
          :href="href(plan)"
        >
          {{ cta(plan) }}
        </a>
        <ul>
          <li>{{ members(plan) }}</li>
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
              v-for="plan in paidPlans"
              :key="plan.code"
            >
              <th scope="row">
                {{ plan.name }} /
                {{ plan.code === 'pro' ? 'Buffer Essentials' : 'Buffer Team' }}
              </th>
              <td>{{ formatMoney(total(plan), locale) }}</td>
              <td>
                {{ formatMoney(bufferBenchmark(plan.code, interval, channels), locale) }}
              </td>
              <td>
                {{ interpolate(copy.saving, {
                  amount: formatMoney(
                    savingsAgainstBuffer(plan, interval, channels),
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

.quantity-control {
  display: grid;
  width: min(30rem, 100%);
  gap: var(--pq-space-2);
  color: var(--pq-color-text-muted);
}

.quantity-control > span {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  color: var(--pq-color-text);
}

.quantity-control output {
  color: var(--pq-color-brand);
  font-size: var(--pq-font-size-xl);
  font-weight: var(--pq-font-weight-bold);
}

.quantity-control input {
  width: 100%;
  min-height: var(--pq-size-target-min);
  accent-color: var(--pq-color-brand);
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
