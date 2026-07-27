<script setup lang="ts">
import {
  computed,
  definePageMeta,
  onBeforeUnmount,
  onMounted,
  ref,
  useAsyncData,
  useHead,
  useRoute,
  useState,
} from '#imports'
import {
  formatMoney,
  localizePath,
  priceForChannels,
  pricingCopy,
} from '../../f02-marketing-site/src/catalog.ts'
import {
  checkoutTransition,
  createIdempotencyKey,
  entitlementConfirmed,
  parsePurchaseIntent,
  safePaddleClientToken,
  type CheckoutSession,
  type CheckoutStatus,
} from '../src/billing.ts'
import {
  checkoutActionForPaddleEvent,
  initializeAndOpenPaddle,
  loadPaddle,
  type PaddleEvent,
} from '../src/paddle.ts'
import {
  useBillingApi,
  useBillingI18n,
  usePaddleClientToken,
} from '../src/use-billing.ts'

definePageMeta({ layout: 'app-shell' })

interface Session {
  current_workspace?: { id: string, role: 'owner' | 'member' }
}

const route = useRoute()
const api = useBillingApi()
const { locale, number, t } = useBillingI18n()
const session = useState<Session | undefined>('postqron.app-shell.session')
const token = usePaddleClientToken()
const status = ref<CheckoutStatus>('idle')
const hostedSession = ref<CheckoutSession>()
const stopped = ref(false)
const idempotencyKeys = useState<Record<string, string>>(
  'postqron.billing-ui.idempotency-keys',
  () => ({}),
)

let intent
try {
  intent = parsePurchaseIntent(route.query)
} catch {
  intent = undefined
}
const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const requestKey = computed(() => intent
  ? `${workspaceId.value}:${intent.plan}:${intent.interval}:${intent.quantity}`
  : 'invalid')
const { data, pending } = await useAsyncData(
  `billing-checkout-${requestKey.value}`,
  async () => ({
    catalog: await api.catalog(),
    overview: await api.overview(workspaceId.value),
  }),
  { immediate: Boolean(intent && workspaceId.value) },
)
const plan = computed(() =>
  data.value?.catalog.plans.find(candidate => candidate.code === intent?.plan))
const planName = computed(() => {
  if (!plan.value) {
    return undefined
  }
  return plan.value.code === 'unlimited'
    ? pricingCopy(locale.value).unlimitedName
    : plan.value.name
})
const total = computed(() =>
  plan.value && intent
    ? priceForChannels(plan.value, intent.interval, intent.quantity ?? null)
    : undefined)
const pricingPath = computed(() => localizePath(locale.value, '/prezzi'))
useHead(computed(() => ({
  title: t('checkout.title', { plan: planName.value ?? intent?.plan ?? '' }),
})))

function transition(action: Parameters<typeof checkoutTransition>[1]) {
  status.value = checkoutTransition(status.value, action)
}

async function confirmEntitlement() {
  if (!intent) {
    return
  }
  for (let attempt = 0; attempt < 15 && !stopped.value; attempt += 1) {
    const overview = await api.overview(workspaceId.value)
    if (entitlementConfirmed(overview, intent)) {
      transition('entitlement-confirmed')
      return
    }
    await new Promise(resolve => globalThis.setTimeout(resolve, 2_000))
  }
}

function paddleEvent(event: PaddleEvent) {
  const action = checkoutActionForPaddleEvent(event)
  if (!action) {
    return
  }
  transition(action)
  if (action === 'completed') {
    void confirmEntitlement()
  }
}

async function openCheckout() {
  if (!intent || intent.plan === 'start' || !plan.value) {
    return
  }
  transition('create')
  try {
    idempotencyKeys.value[requestKey.value] ??= createIdempotencyKey()
    hostedSession.value = await api.checkout(
      workspaceId.value,
      intent,
      idempotencyKeys.value[requestKey.value]!,
    )
    const clientToken = safePaddleClientToken(token.value)
    if (!clientToken) {
      throw new Error('PADDLE_CLIENT_TOKEN_UNAVAILABLE')
    }
    const paddle = await loadPaddle(globalThis.window, globalThis.document)
    initializeAndOpenPaddle(paddle, {
      token: clientToken,
      locale: locale.value,
      transactionId: hostedSession.value.id,
      eventCallback: paddleEvent,
    })
    transition('opened')
  } catch {
    transition('error')
  }
}

onMounted(() => {
  if (intent?.plan !== 'start' && plan.value) {
    void openCheckout()
  }
})
onBeforeUnmount(() => {
  stopped.value = true
})
</script>

<template>
  <section class="billing-page">
    <header>
      <p class="app-eyebrow">
        {{ t('checkout.eyebrow') }}
      </p>
      <h1>
        {{ t('checkout.title', { plan: planName ?? intent?.plan ?? '' }) }}
      </h1>
      <p class="billing-page__lead">
        {{ t('checkout.description') }}
      </p>
    </header>
    <div
      v-if="!intent"
      class="billing-state"
      role="alert"
    >
      <p>{{ t('checkout.invalid') }}</p>
      <NuxtLink
        class="pq-button"
        :to="pricingPath"
      >
        {{ t('checkout.back') }}
      </NuxtLink>
    </div>
    <div
      v-else-if="pending || !plan"
      class="billing-state"
      role="status"
    >
      {{ t('checkout.loading') }}
    </div>
    <article
      v-else
      class="billing-card"
    >
      <div class="billing-card__header">
        <h2>{{ planName }}</h2>
        <span class="billing-badge">{{ t(`interval.${intent.interval}`) }}</span>
      </div>
      <p v-if="intent.quantity !== undefined">
        {{ t('checkout.channels', { count: number(intent.quantity) }) }}
      </p>
      <p v-else>
        {{ t('checkout.flatRate') }}
      </p>
      <p v-if="total">
        <strong>{{ t('checkout.baseTotal', {
          total: formatMoney(total, locale),
        }) }}</strong>
      </p>
      <p class="billing-summary">
        {{ t('checkout.taxes') }}
      </p>
      <div
        v-if="intent.plan === 'start'"
        class="billing-state"
        role="status"
      >
        <p>{{ t('checkout.free') }}</p>
      </div>
      <div
        v-else
        class="billing-state"
        :role="status === 'error' || status === 'payment-failed' ? 'alert' : 'status'"
        aria-live="polite"
      >
        <p v-if="status === 'creating' || status === 'open'">
          {{ t('checkout.opening') }}
        </p>
        <p v-else-if="status === 'closed'">
          {{ t('checkout.closed') }}
        </p>
        <p v-else-if="status === 'payment-failed'">
          {{ t('checkout.paymentFailed') }}
        </p>
        <p v-else-if="status === 'processing'">
          {{ t('checkout.processing') }}
        </p>
        <p v-else-if="status === 'confirmed'">
          {{ t('checkout.confirmed') }}
        </p>
        <p v-else-if="status === 'error'">
          {{ t('checkout.error') }}
        </p>
        <div class="billing-actions">
          <button
            v-if="['idle', 'closed', 'payment-failed', 'error'].includes(status)"
            class="pq-button"
            type="button"
            @click="openCheckout"
          >
            {{ status === 'idle' ? t('checkout.open') : t('checkout.retry') }}
          </button>
          <a
            v-if="hostedSession && status === 'error'"
            class="pq-button pq-button--secondary"
            :href="hostedSession.url"
          >
            {{ t('checkout.fallback') }}
          </a>
        </div>
      </div>
      <div class="billing-actions">
        <NuxtLink :to="pricingPath">
          {{ t('checkout.back') }}
        </NuxtLink>
      </div>
    </article>
  </section>
</template>
