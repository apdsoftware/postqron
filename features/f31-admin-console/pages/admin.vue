<script setup lang="ts">
import {
  computed,
  definePageMeta,
  useHead,
} from '#imports'
import AdminAlert from '../components/AdminAlert.vue'
import AdminKpiCards, { type AdminKpi } from '../components/AdminKpiCards.vue'
import AdminPageHeader from '../components/AdminPageHeader.vue'
import { useAdminSectionLoad } from '../components/use-admin-section.ts'
import {
  useAdminApi,
  useAdminDashboardState,
  useAdminI18n,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

// Controlled background refresh, not an aggressive loop: one request a
// minute, cancelled on navigation and backed off on repeated failure.
const dashboardRefreshIntervalMs = 60_000

const api = useAdminApi()
const dashboard = useAdminDashboardState()
const { date, t } = useAdminI18n()
const { loading, errorCode, reload } = useAdminSectionLoad(
  dashboard,
  signal => api.dashboard({ signal }),
  { intervalMs: dashboardRefreshIntervalMs },
)

useHead(computed(() => ({
  title: t('document.title'),
})))

function healthStatusLabel(status: string): string {
  switch (status) {
    case 'operational':
      return t('health.healthy')
    case 'degraded':
      return t('health.degraded')
    case 'outage':
      return t('health.outage')
    default:
      return t('health.unknown')
  }
}

const degradedServices = computed(() => (dashboard.value?.services ?? [])
  .filter(service => service.status !== 'operational'))

const latestDegradedCheckedAt = computed(() => {
  const timestamps = degradedServices.value.map(service => service.checked_at).sort()
  return timestamps.at(-1)
})

const kpis = computed<AdminKpi[]>(() => {
  const data = dashboard.value
  if (!data) {
    return []
  }
  const healthy = data.services.filter(service => service.status === 'operational').length
  const internal = data.entitlements.filter(entitlement => entitlement.internal).length
  return [
    {
      key: 'services',
      label: t('dashboard.kpi.services'),
      value: String(data.services.length),
    },
    {
      key: 'healthy',
      label: t('dashboard.kpi.healthy'),
      value: `${healthy}/${data.services.length}`,
      tone: healthy === data.services.length ? 'success' : 'warning',
    },
    {
      key: 'workspaces',
      label: t('dashboard.kpi.workspaces'),
      value: String(data.entitlements.length),
    },
    {
      key: 'internal',
      label: t('dashboard.kpi.internalPlans'),
      value: String(internal),
    },
    {
      key: 'audit',
      label: t('dashboard.kpi.auditEvents'),
      value: String(data.recent_audit.length),
    },
  ]
})
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('dashboard.title')"
      :description="t('dashboard.description')"
    />

    <AdminAlert
      v-if="degradedServices.length > 0"
      variant="error"
    >
      {{ t('dashboard.alertDegraded', {
        services: degradedServices.map(service => service.code).join(', '),
        checkedAt: latestDegradedCheckedAt ? date(latestDegradedCheckedAt) : '',
      }) }}
    </AdminAlert>

    <AdminAlert
      v-if="loading && !dashboard"
      variant="info"
    >
      {{ t('status.loading') }}
    </AdminAlert>
    <AdminAlert
      v-else-if="errorCode && !dashboard"
      variant="error"
    >
      {{ t(`error.${errorCode}` as never) }}
      <button
        class="pq-button pq-button--secondary"
        type="button"
        @click="reload"
      >
        {{ t('status.retry') }}
      </button>
    </AdminAlert>

    <template v-else-if="dashboard">
      <AdminAlert
        v-if="errorCode"
        variant="error"
      >
        {{ t(`error.${errorCode}` as never) }}
        <button
          class="pq-button pq-button--secondary"
          type="button"
          @click="reload"
        >
          {{ t('status.retry') }}
        </button>
      </AdminAlert>

      <AdminKpiCards :items="kpis" />

      <section
        class="admin-panel"
        aria-labelledby="admin-health-title"
      >
        <h2 id="admin-health-title">
          {{ t('health.title') }}
        </h2>
        <ul class="admin-health">
          <li
            v-for="service in dashboard.services"
            :key="service.code"
          >
            <span
              class="admin-status-dot"
              :data-status="service.status"
              aria-hidden="true"
            />
            <strong>{{ service.code }}</strong>
            <span>{{ healthStatusLabel(service.status) }}</span>
            <time :datetime="service.checked_at">{{ date(service.checked_at) }}</time>
          </li>
        </ul>
      </section>
    </template>
  </section>
</template>
