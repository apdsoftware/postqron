<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useAsyncData,
  useState,
} from '#imports'
import { createIdempotencyKey } from '../src/billing.ts'
import { useBillingApi, useBillingI18n } from '../src/use-billing.ts'

definePageMeta({ layout: 'app-shell' })

interface Session {
  current_workspace?: { id: string, role: 'owner' | 'member' }
}

const session = useState<Session | undefined>('postqron.app-shell.session')
const api = useBillingApi()
const { date, number, t } = useBillingI18n()
const portalOpening = ref(false)
const portalError = ref(false)
const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const isOwner = computed(() => session.value?.current_workspace?.role === 'owner')
const { data: overview, pending, refresh, status } = await useAsyncData(
  'billing-overview',
  () => api.overview(workspaceId.value),
)

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
</script>

<template>
  <section class="billing-page">
    <header>
      <p class="app-eyebrow">
        {{ t('overview.eyebrow') }}
      </p>
      <h1>{{ t('overview.title') }}</h1>
      <p class="billing-page__lead">
        {{ t('overview.description') }}
      </p>
    </header>
    <div
      v-if="pending"
      class="billing-state"
      role="status"
    >
      {{ t('checkout.loading') }}
    </div>
    <div
      v-else-if="status === 'error' || !overview"
      class="billing-state"
      role="alert"
    >
      <p>{{ t('checkout.error') }}</p>
      <button
        class="pq-button"
        type="button"
        @click="refresh"
      >
        {{ t('checkout.retry') }}
      </button>
    </div>
    <article
      v-else
      class="billing-card"
    >
      <div class="billing-card__header">
        <div>
          <small>{{ t('overview.currentPlan') }}</small>
          <h2>{{ overview.plan.name }}</h2>
        </div>
        <span class="billing-badge">{{ t(`state.${overview.state}`) }}</span>
      </div>
      <p class="billing-summary">
        {{ t('overview.interval') }}:
        {{ t(`interval.${overview.interval}`) }}
      </p>
      <p class="billing-summary">
        {{ t('overview.period', {
          start: date(overview.period.start),
          end: date(overview.period.end),
        }) }}
      </p>
      <div class="billing-usage">
        <article
          v-for="usage in overview.usage"
          :key="usage.resource"
        >
          <strong>{{ t(`resource.${usage.resource}`) }}</strong>
          <span>{{ t('overview.used', {
            used: number(usage.used),
            limit: number(usage.limit),
            remaining: number(usage.remaining),
          }) }}</span>
        </article>
      </div>
      <p
        v-if="portalError"
        role="alert"
      >
        {{ t('overview.portalError') }}
      </p>
      <div class="billing-actions">
        <button
          v-if="overview.plan.purchasable && isOwner"
          class="pq-button"
          type="button"
          :disabled="portalOpening"
          @click="openPortal"
        >
          {{ portalOpening ? t('overview.portalOpening') : t('overview.portal') }}
        </button>
        <span v-else-if="!overview.plan.purchasable">
          {{ t('overview.noPortal') }}
        </span>
        <NuxtLink
          class="pq-button pq-button--secondary"
          to="/prezzi"
        >
          {{ t('overview.pricing') }}
        </NuxtLink>
      </div>
    </article>
  </section>
</template>
