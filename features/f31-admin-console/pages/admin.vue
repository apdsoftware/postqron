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

const api = useAdminApi()
const dashboard = useAdminDashboardState()
const { date, t } = useAdminI18n()
const { loading, errorCode, reload } = useAdminSectionLoad(
  dashboard,
  () => api.dashboard(),
)

useHead(computed(() => ({
  title: t('document.title'),
})))

const kpis = computed<AdminKpi[]>(() => {
  const data = dashboard.value
  if (!data) {
    return []
  }
  const healthy = data.services.filter(service => service.status === 'healthy').length
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
      v-if="loading"
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
            <span>{{ service.status === 'healthy' ? t('health.healthy') : t('health.degraded') }}</span>
            <time :datetime="service.checked_at">{{ date(service.checked_at) }}</time>
          </li>
        </ul>
      </section>
    </template>
  </section>
</template>
