<script setup lang="ts">
/* global HTMLButtonElement, HTMLElement, KeyboardEvent */
import {
  computed,
  navigateTo,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from '#imports'
import {
  formatMoney,
  pricingCopy,
  type BillingInterval,
  type PricingLocale,
  type PublicCatalog,
  type PublicPlan,
  type PublicPlanCode,
} from '../../f02-marketing-site/src/catalog.ts'
import {
  checkoutPath,
  createIdempotencyKey,
  PlanChangeApiError,
  type BillingOverview,
  type DowngradeOverage,
  type PlanChangeIntent,
  type SubscriptionChangePreview,
} from '../src/billing.ts'
import {
  compatibilityForPlan,
  intentForPlan,
  minimumCompatiblePlan,
  overviewMatchesTarget,
  priceForIntent,
  providerPreviewAmounts,
  recommendedChannelQuantity,
  sameAsCurrentPlan,
} from '../src/plan-change.ts'
import {
  useBillingApi,
  useBillingPlanI18n,
} from '../src/use-billing.ts'

const props = defineProps<{
  catalog: PublicCatalog
  overview: BillingOverview
  workspaceId: string
}>()

const emit = defineEmits<{
  changed: []
  close: []
}>()

type FlowState =
  | 'editing'
  | 'previewing'
  | 'previewed'
  | 'requesting'
  | 'pending'
  | 'success'
  | 'error'

const api = useBillingApi()
const { locale, number, t } = useBillingPlanI18n()
const minimumPlan = computed(() =>
  minimumCompatiblePlan(props.catalog, props.overview))
const selectedCode = ref<PublicPlanCode>(minimumPlan.value.code)
const interval = ref<BillingInterval>(props.overview.interval)
const selectedChannels = ref<number>()
const state = ref<FlowState>('editing')
const preview = ref<SubscriptionChangePreview>()
const overages = ref<DowngradeOverage[]>([])
const errorCode = ref<PlanChangeApiError['code']>()
const idempotencyKey = ref('')
const closeButton = ref<HTMLButtonElement>()
const dialogElement = ref<HTMLElement>()
let stopped = false

const selectedPlan = computed(() =>
  props.catalog.plans.find(plan => plan.code === selectedCode.value)!)
const selectedCompatibility = computed(() =>
  compatibilityForPlan(selectedPlan.value, props.overview))
const intent = computed(() => intentForPlan(
  selectedPlan.value,
  interval.value,
  props.overview,
  selectedChannels.value,
))
const isCurrent = computed(() =>
  sameAsCurrentPlan(props.overview, intent.value))
const isStartCheckout = computed(() =>
  props.overview.plan.code === 'start' && selectedCode.value !== 'start')
const previewAmounts = computed(() =>
  preview.value ? providerPreviewAmounts(preview.value) : undefined)
const selectedPrice = computed(() =>
  priceForIntent(selectedPlan.value, intent.value))
const channelOptions = computed(() => {
  const maximum = selectedPlan.value.limits.channels ?? 0
  return Array.from({ length: maximum }, (_value, index) => index + 1)
})

function planName(plan: PublicPlan): string {
  return plan.code === 'unlimited'
    ? pricingCopy(locale.value).unlimitedName
    : plan.name
}

function compatibility(plan: PublicPlan) {
  return compatibilityForPlan(plan, props.overview)
}

function planIntent(plan: PublicPlan): PlanChangeIntent {
  return intentForPlan(plan, interval.value, props.overview)
}

function planPrice(plan: PublicPlan): string {
  return formatMoney(priceForIntent(plan, planIntent(plan)), locale.value)
}

function resetFlow() {
  state.value = 'editing'
  preview.value = undefined
  overages.value = []
  errorCode.value = undefined
  idempotencyKey.value = ''
}

function selectPlan(plan: PublicPlan) {
  if (!compatibility(plan).compatible) {
    return
  }
  selectedCode.value = plan.code
}

watch([selectedCode, interval], () => {
  selectedChannels.value = recommendedChannelQuantity(
    selectedPlan.value,
    props.overview,
  )
  resetFlow()
}, { immediate: true })

watch(selectedChannels, (_current, previous) => {
  if (previous !== undefined) {
    resetFlow()
  }
})

function blockedAction(overage: DowngradeOverage): string {
  return t(`blocked.${overage.resource}`, {
    excess: number(overage.excess),
  })
}

function showError(error: unknown) {
  state.value = 'error'
  if (!(error instanceof PlanChangeApiError)) {
    errorCode.value = 'unknown'
    return
  }
  errorCode.value = error.code
  overages.value = error.overages
}

async function reviewChange() {
  if (isCurrent.value || !selectedCompatibility.value.compatible) {
    return
  }
  if (isStartCheckout.value) {
    await goToCheckout()
    return
  }
  resetFlow()
  state.value = 'previewing'
  idempotencyKey.value = createIdempotencyKey()
  try {
    preview.value = await api.previewSubscriptionChange(
      props.workspaceId,
      intent.value,
      idempotencyKey.value,
    )
    state.value = 'previewed'
    await nextTick()
  } catch (error) {
    showError(error)
  }
}

async function goToCheckout() {
  const purchase = intent.value
  if (purchase.plan === 'start' || !purchase.interval) {
    return
  }
  await navigateTo(checkoutPath(locale.value as PricingLocale, {
    plan: purchase.plan,
    interval: purchase.interval,
    quantity: purchase.channels,
  }))
}

async function waitForAppliedChange(target: SubscriptionChangePreview['target']) {
  for (let attempt = 0; attempt < 15 && !stopped; attempt += 1) {
    try {
      const current = await api.overview(props.workspaceId)
      if (overviewMatchesTarget(current, target)) {
        state.value = 'success'
        emit('changed')
        return
      }
    } catch {
      // A temporary refresh failure does not invalidate the durable request.
    }
    await new Promise(resolve => globalThis.setTimeout(resolve, 2_000))
  }
}

async function confirmChange() {
  if (!preview.value || !idempotencyKey.value) {
    return
  }
  state.value = 'requesting'
  errorCode.value = undefined
  overages.value = []
  try {
    const result = await api.applySubscriptionChange(
      props.workspaceId,
      intent.value,
      idempotencyKey.value,
    )
    if (result.status === 'applied') {
      state.value = 'success'
      emit('changed')
      return
    }
    state.value = 'pending'
    if (preview.value.immediate) {
      void waitForAppliedChange(preview.value.target)
    }
  } catch (error) {
    showError(error)
  }
}

function retry() {
  if (errorCode.value === 'checkout_required') {
    void goToCheckout()
    return
  }
  if (preview.value && idempotencyKey.value && overages.value.length === 0) {
    void confirmChange()
    return
  }
  void reviewChange()
}

function errorMessage(): string {
  if (errorCode.value === 'plan_change_in_progress') {
    return t('change.inProgress')
  }
  if (errorCode.value === 'plan_change_conflict'
    || errorCode.value === 'idempotency_conflict') {
    return t('change.conflict')
  }
  if (errorCode.value === 'plan_already_active') {
    return t('change.samePlan')
  }
  if (errorCode.value === 'checkout_required') {
    return t('change.checkoutRequired')
  }
  return preview.value ? t('change.failure') : t('change.previewError')
}

function close() {
  emit('close')
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key !== 'Tab' || !dialogElement.value) {
    return
  }
  const focusable = [...dialogElement.value.querySelectorAll<HTMLElement>(
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), a[href]',
  )]
  if (focusable.length === 0) {
    return
  }
  const first = focusable[0]!
  const last = focusable.at(-1)!
  if (event.shiftKey && globalThis.document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && globalThis.document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(() => {
  globalThis.addEventListener('keydown', onKeydown)
  closeButton.value?.focus()
})

onBeforeUnmount(() => {
  stopped = true
  globalThis.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div
    class="plan-dialog__backdrop"
    @click.self="close"
  >
    <section
      ref="dialogElement"
      class="plan-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="plan-change-title"
      aria-describedby="plan-change-description"
    >
      <header class="plan-dialog__header">
        <div>
          <p class="app-eyebrow">
            {{ t('change.dialog') }}
          </p>
          <h2 id="plan-change-title">
            {{ t('change.title') }}
          </h2>
          <p id="plan-change-description">
            {{ t('change.description') }}
          </p>
        </div>
        <button
          ref="closeButton"
          class="plan-dialog__close"
          type="button"
          :aria-label="t('change.close')"
          @click="close"
        >
          <span aria-hidden="true">×</span>
        </button>
      </header>

      <fieldset class="plan-interval">
        <legend>{{ t('change.interval') }}</legend>
        <label
          v-for="candidate in (['monthly', 'annual'] as const)"
          :key="candidate"
        >
          <input
            v-model="interval"
            type="radio"
            name="billing-plan-interval"
            :value="candidate"
            :disabled="state === 'requesting'"
          >
          <span>{{ t(`interval.${candidate}`) }}</span>
        </label>
        <small v-if="interval === 'annual'">{{ t('change.annualNote') }}</small>
      </fieldset>

      <div
        class="plan-comparison"
        role="list"
      >
        <article
          v-for="plan in catalog.plans"
          :key="plan.code"
          class="plan-option"
          :class="{
            'plan-option--selected': selectedCode === plan.code,
            'plan-option--incompatible': !compatibility(plan).compatible,
          }"
          role="listitem"
        >
          <div class="plan-option__labels">
            <span
              v-if="plan.code === minimumPlan.code"
              class="billing-badge"
            >{{ t('change.recommended') }}</span>
            <span
              v-if="plan.code === overview.plan.code"
              class="billing-badge billing-badge--neutral"
            >{{ t('change.current') }}</span>
          </div>
          <h3>{{ planName(plan) }}</h3>
          <strong class="plan-option__price">
            {{ t('change.basePrice', { price: planPrice(plan) }) }}
          </strong>
          <ul class="plan-option__limits">
            <li>
              {{ plan.limits.members === null
                ? t('change.unlimitedMembers')
                : t('change.members', { count: number(plan.limits.members) }) }}
            </li>
            <li>
              {{ plan.limits.channels === null
                ? t('change.unlimitedChannels')
                : t('change.channels', { count: number(plan.limits.channels) }) }}
            </li>
            <li>
              {{ plan.limits.scheduled_publications === null
                ? t('change.unlimitedPosts')
                : t('change.posts', { count: number(plan.limits.scheduled_publications) }) }}
            </li>
          </ul>
          <button
            class="pq-button pq-button--secondary"
            type="button"
            :aria-pressed="selectedCode === plan.code"
            :disabled="!compatibility(plan).compatible || state === 'requesting'"
            @click="selectPlan(plan)"
          >
            {{ selectedCode === plan.code
              ? t('change.selected', { plan: planName(plan) })
              : t('change.select', { plan: planName(plan) }) }}
          </button>
          <p
            v-if="!compatibility(plan).compatible"
            class="plan-option__warning"
          >
            {{ t('change.incompatible') }}
          </p>
          <ul
            v-if="!compatibility(plan).compatible"
            class="plan-option__overages"
          >
            <li
              v-for="overage in compatibility(plan).overages"
              :key="overage.resource"
            >
              {{ t('blocked.values', {
                resource: t(`resource.${overage.resource}`),
                used: number(overage.used),
                limit: number(overage.limit),
                excess: number(overage.excess),
              }) }}
              {{ blockedAction(overage) }}
            </li>
          </ul>
        </article>
      </div>

      <div
        v-if="selectedPlan.code === 'pro' || selectedPlan.code === 'team'"
        class="plan-quantity"
      >
        <label for="plan-channel-quantity">{{ t('change.quantity') }}</label>
        <select
          id="plan-channel-quantity"
          v-model.number="selectedChannels"
          :disabled="state === 'requesting'"
        >
          <option
            v-for="quantity in channelOptions"
            :key="quantity"
            :value="quantity"
          >
            {{ number(quantity) }}
          </option>
        </select>
      </div>

      <div class="plan-selection-summary">
        <strong>{{ planName(selectedPlan) }}</strong>
        <span>{{ t(`interval.${intent.interval ?? 'monthly'}`) }}</span>
        <span>{{ formatMoney(selectedPrice, locale) }}</span>
        <small>{{ t('change.taxNotice') }}</small>
      </div>

      <section
        v-if="overages.length > 0"
        class="plan-change-state plan-change-state--blocked"
        role="alert"
        aria-labelledby="blocked-title"
      >
        <h3 id="blocked-title">
          {{ t('blocked.title') }}
        </h3>
        <p>{{ t('blocked.description') }}</p>
        <ul>
          <li
            v-for="overage in overages"
            :key="overage.resource"
          >
            <strong>{{ t('blocked.values', {
              resource: t(`resource.${overage.resource}`),
              used: number(overage.used),
              limit: number(overage.limit),
              excess: number(overage.excess),
            }) }}</strong>
            <span>{{ blockedAction(overage) }}</span>
          </li>
        </ul>
        <p>{{ t('blocked.noDeletion') }}</p>
      </section>

      <section
        v-if="preview"
        class="plan-change-state"
        aria-live="polite"
      >
        <h3>
          {{ preview.direction === 'upgrade'
            ? t('change.upgrade')
            : t('change.downgrade') }}
        </h3>
        <p>
          {{ preview.immediate ? t('change.immediate') : t('change.periodEnd') }}
        </p>
        <p v-if="preview.action === 'cancel_subscription'">
          {{ t('change.cancelAtPeriodEnd') }}
        </p>
        <p v-if="previewAmounts?.immediate">
          {{ t('change.chargeNow', {
            amount: formatMoney(previewAmounts.immediate, locale),
          }) }}
        </p>
        <p v-if="previewAmounts?.recurring">
          {{ t('change.recurring', {
            amount: formatMoney(previewAmounts.recurring, locale),
          }) }}
        </p>
      </section>

      <div
        v-if="state === 'previewing'"
        class="plan-change-state"
        role="status"
      >
        {{ t('change.previewing') }}
      </div>
      <div
        v-else-if="state === 'requesting'"
        class="plan-change-state"
        role="status"
      >
        {{ t('change.requesting') }}
      </div>
      <div
        v-else-if="state === 'pending'"
        class="plan-change-state"
        role="status"
        aria-live="polite"
      >
        {{ t('change.pending') }}
      </div>
      <div
        v-else-if="state === 'success'"
        class="plan-change-state plan-change-state--success"
        role="status"
        aria-live="polite"
      >
        {{ t('change.success') }}
      </div>
      <div
        v-else-if="state === 'error' && overages.length === 0"
        class="plan-change-state plan-change-state--error"
        role="alert"
      >
        {{ errorMessage() }}
      </div>

      <footer class="plan-dialog__actions">
        <button
          class="pq-button pq-button--secondary"
          type="button"
          @click="close"
        >
          {{ t('change.close') }}
        </button>
        <button
          v-if="state === 'editing' || state === 'error'"
          class="pq-button"
          type="button"
          :disabled="isCurrent || !selectedCompatibility.compatible"
          @click="state === 'error' ? retry() : reviewChange()"
        >
          {{ state === 'error'
            ? errorCode === 'checkout_required'
              ? t('change.checkout')
              : t('common.retry')
            : isStartCheckout
              ? t('change.checkout')
              : t('change.preview') }}
        </button>
        <button
          v-if="state === 'previewed'"
          class="pq-button"
          type="button"
          @click="confirmChange"
        >
          {{ t('change.confirm') }}
        </button>
      </footer>
      <p
        v-if="isCurrent && state === 'editing'"
        class="plan-dialog__hint"
        role="status"
      >
        {{ t('change.samePlan') }}
      </p>
    </section>
  </div>
</template>
