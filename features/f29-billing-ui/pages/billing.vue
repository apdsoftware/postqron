<script setup lang="ts">
/* global HTMLButtonElement */
import {
  computed,
  definePageMeta,
  nextTick,
  onMounted,
  ref,
  useAsyncData,
  useHead,
  useState,
  watch,
} from '#imports'
import {
  formatMoney,
  pricingCopy,
} from '../../f02-marketing-site/src/catalog.ts'
import PlanChangeDialog from '../components/PlanChangeDialog.vue'
import {
  createIdempotencyKey,
  type BillingUsage,
} from '../src/billing.ts'
import {
  currentPlanPrice,
  usagePercentage,
} from '../src/plan-change.ts'
import {
  useBillingApi,
  useBillingPlanI18n,
} from '../src/use-billing.ts'

definePageMeta({ layout: 'app-shell' })

interface Session {
  current_workspace?: { id: string, role: 'owner' | 'member' }
}

const session = useState<Session | undefined>('postqron.app-shell.session')
const api = useBillingApi()
const { date, locale, number, t } = useBillingPlanI18n()
const portalOpening = ref(false)
const portalError = ref(false)
const changingPlan = ref(false)
const changeButton = ref<HTMLButtonElement>()
const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const isOwner = computed(() => session.value?.current_workspace?.role === 'owner')

useHead(computed(() => ({ title: t('document.title') })))

const {
  data,
  pending,
  refresh,
  status,
} = useAsyncData(
  'billing-plan-overview',
  async () => {
    if (!workspaceId.value) {
      return undefined
    }
    const [catalog, overview] = await Promise.all([
      api.catalog(),
      api.overview(workspaceId.value),
    ])
    return { catalog, overview }
  },
  { immediate: false, server: false },
)

async function loadPlan() {
  if (workspaceId.value) {
    await refresh()
  }
}

onMounted(() => {
  void loadPlan()
})

watch(workspaceId, (current, previous) => {
  if (current && current !== previous) {
    changingPlan.value = false
    void loadPlan()
  }
})

const overview = computed(() => data.value?.overview)
const catalog = computed(() => data.value?.catalog)
const planName = computed(() => {
  if (overview.value?.plan.code === 'unlimited') {
    return pricingCopy(locale.value).unlimitedName
  }
  return overview.value?.plan.name ?? ''
})
const price = computed(() =>
  overview.value ? currentPlanPrice(overview.value) : undefined)
const channels = computed(() =>
  overview.value?.usage.find(usage => usage.resource === 'channels'))
const hasManagedSubscription = computed(() =>
  Boolean(overview.value?.plan.purchasable
    && !['trialing', 'trial_expired', 'canceled'].includes(
      overview.value?.state ?? '',
    )))

function periodMessage(): string {
  if (!overview.value) {
    return ''
  }
  if (overview.value.state === 'trialing') {
    return t('overview.trialEnds', { date: date(overview.value.period.end) })
  }
  if (overview.value.state === 'active'
    || overview.value.state === 'past_due') {
    return t('overview.renews', { date: date(overview.value.period.end) })
  }
  return t('overview.periodEnds', { date: date(overview.value.period.end) })
}

function usageStyle(usage: BillingUsage): Record<string, string> {
  const percentage = usagePercentage(usage)
  return {
    '--billing-usage': `${Math.min(100, percentage ?? 0)}%`,
  }
}

function usageAria(usage: BillingUsage): string {
  const resource = t(`resource.${usage.resource}`)
  if (usage.limit === null) {
    return `${resource}: ${t('usage.value', { used: number(usage.used) })}, ${t('usage.unlimited')}`
  }
  return `${resource}: ${t('usage.value', { used: number(usage.used) })}, ${t('usage.limit', { limit: number(usage.limit) })}, ${t('usage.remaining', { remaining: number(usage.remaining ?? 0) })}`
}

async function openPortal() {
  portalOpening.value = true
  portalError.value = false
  try {
    const portal = await api.portal(workspaceId.value, createIdempotencyKey())
    globalThis.location.assign(portal.url)
  } catch {
    portalError.value = true
  } finally {
    portalOpening.value = false
  }
}

function openPlanChange() {
  changingPlan.value = true
}

async function closePlanChange() {
  changingPlan.value = false
  await nextTick()
  changeButton.value?.focus()
}

async function planChanged() {
  await refresh()
}
</script>

<template>
  <section class="billing-page billing-plan-page">
    <header class="billing-plan-page__header">
      <p class="app-eyebrow">
        {{ t('overview.eyebrow') }}
      </p>
      <h1>{{ t('overview.title') }}</h1>
      <p class="billing-page__lead">
        {{ t('overview.description') }}
      </p>
    </header>

    <div
      v-if="pending || status === 'idle'"
      class="billing-state billing-state--loading"
      role="status"
      aria-live="polite"
    >
      <span
        class="billing-spinner"
        aria-hidden="true"
      />
      {{ t('common.loading') }}
    </div>

    <div
      v-else-if="status === 'error' || !overview || !catalog"
      class="billing-state"
      role="alert"
    >
      <p>{{ t('common.error') }}</p>
      <button
        class="pq-button"
        type="button"
        :disabled="!workspaceId"
        @click="refresh"
      >
        {{ t('common.retry') }}
      </button>
    </div>

    <template v-else>
      <article class="billing-card billing-overview-card">
        <div class="billing-card__header">
          <div>
            <small>{{ t('overview.current') }}</small>
            <div class="billing-overview-card__title">
              <h2>{{ planName }}</h2>
              <span class="billing-badge">{{ t(`state.${overview.state}`) }}</span>
            </div>
          </div>
          <div
            v-if="isOwner"
            class="billing-actions"
          >
            <button
              ref="changeButton"
              class="pq-button"
              type="button"
              @click="openPlanChange"
            >
              {{ t('overview.change') }}
            </button>
            <button
              v-if="hasManagedSubscription"
              class="pq-button pq-button--secondary"
              type="button"
              :disabled="portalOpening"
              @click="openPortal"
            >
              {{ portalOpening ? t('overview.managing') : t('overview.manage') }}
            </button>
          </div>
        </div>

        <dl class="billing-overview-card__facts">
          <div>
            <dt>{{ t('overview.interval') }}</dt>
            <dd>{{ t(`interval.${overview.interval}`) }}</dd>
          </div>
          <div>
            <dt>{{ t('overview.channels') }}</dt>
            <dd>
              {{ channels?.limit === null
                ? t('usage.unlimited')
                : number(channels?.limit ?? overview.plan.limits.channels ?? 0) }}
            </dd>
          </div>
          <div>
            <dt>{{ t('overview.price') }}</dt>
            <dd>
              {{ overview.state === 'trialing' || price?.amount_cents === 0
                ? t('overview.free')
                : price
                  ? formatMoney(price, locale)
                  : '—' }}
            </dd>
          </div>
        </dl>

        <p class="billing-overview-card__period">
          {{ periodMessage() }}
        </p>
        <p
          v-if="portalError"
          class="billing-inline-error"
          role="alert"
        >
          {{ t('overview.manageError') }}
        </p>
        <p v-if="isOwner && !hasManagedSubscription">
          {{ t('overview.noSubscription') }}
        </p>
        <p
          v-if="!isOwner"
          class="billing-read-only"
        >
          {{ t('overview.readOnly') }}
        </p>
      </article>

      <section
        class="billing-usage-section"
        aria-labelledby="billing-usage-title"
      >
        <header>
          <h2 id="billing-usage-title">
            {{ t('usage.title') }}
          </h2>
          <p>{{ t('usage.description') }}</p>
        </header>
        <div class="billing-usage">
          <article
            v-for="usage in overview.usage"
            :key="usage.resource"
            class="billing-usage-card"
          >
            <div class="billing-usage-card__header">
              <h3>{{ t(`resource.${usage.resource}`) }}</h3>
              <strong>
                {{ usage.limit === null
                  ? t('usage.unlimited')
                  : t('usage.percent', { percent: number(usagePercentage(usage) ?? 0) }) }}
              </strong>
            </div>
            <div
              class="billing-progress"
              :class="{ 'billing-progress--unlimited': usage.limit === null }"
              :style="usageStyle(usage)"
              :role="usage.limit === null ? undefined : 'progressbar'"
              :aria-label="usageAria(usage)"
              :aria-valuemin="usage.limit === null ? undefined : 0"
              :aria-valuemax="usage.limit === null ? undefined : 100"
              :aria-valuenow="usage.limit === null
                ? undefined
                : Math.min(100, usagePercentage(usage) ?? 0)"
            >
              <span />
            </div>
            <dl>
              <div>
                <dt>{{ t('usage.value', { used: number(usage.used) }) }}</dt>
              </div>
              <div v-if="usage.limit !== null">
                <dt>{{ t('usage.limit', { limit: number(usage.limit) }) }}</dt>
              </div>
              <div>
                <dt>
                  {{ usage.remaining === null
                    ? t('usage.unlimited')
                    : t('usage.remaining', { remaining: number(usage.remaining) }) }}
                </dt>
              </div>
            </dl>
          </article>
        </div>
      </section>
    </template>

    <PlanChangeDialog
      v-if="changingPlan && overview && catalog"
      :catalog="catalog"
      :overview="overview"
      :workspace-id="workspaceId"
      @changed="planChanged"
      @close="closePlanChange"
    />
  </section>
</template>
