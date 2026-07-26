<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useHead,
} from '#imports'
import { AdminApiError, normalizeAdminApiError } from '../core/api.ts'
import {
  useAdminApi,
  useAdminDashboardState,
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'
import { ADMIN_NAV_ITEMS } from '../components/nav.ts'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const api = useAdminApi()
const session = useAdminSessionState()
const dashboard = useAdminDashboardState()
const { locale, t } = useAdminI18n()
const loading = ref(true)
const errorCode = ref<AdminApiError['code']>()

useHead(computed(() => ({
  title: t('document.title'),
})))

const healthSummary = computed(() => {
  const services = dashboard.value?.services ?? []
  return {
    healthy: services.filter(service => service.status === 'healthy').length,
    total: services.length,
  }
})

const quickLinks = computed(() => ADMIN_NAV_ITEMS
  .filter(item => item.path !== '/admin')
  .map(item => ({
    ...item,
    href: localizeUrl(locale.value as 'de' | 'en' | 'es' | 'fr' | 'it', item.path),
  })))

async function loadDashboard() {
  loading.value = true
  errorCode.value = undefined
  try {
    dashboard.value = await api.dashboard()
  } catch (error) {
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    loading.value = false
  }
}

if (import.meta.client && session.value) {
  await loadDashboard()
} else {
  loading.value = false
}
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('page.title')"
      :description="t('page.description')"
    />

    <AdminLoginGate
      v-if="!session"
      @authenticated="loadDashboard"
    />

    <AdminState
      v-else-if="loading"
      variant="loading"
      :message="t('status.loading')"
    />
    <AdminState
      v-else-if="errorCode && !dashboard"
      variant="error"
      :message="t(`error.${errorCode}` as never)"
      :retry-label="t('status.retry')"
      @retry="loadDashboard"
    />

    <template v-else-if="dashboard">
      <div class="admin-kpi-grid">
        <AdminKpiCard
          :label="t('dashboard.kpi.services')"
          :value="`${healthSummary.healthy}/${healthSummary.total}`"
          :hint="t('dashboard.kpi.servicesHint')"
          :status="healthSummary.healthy === healthSummary.total ? 'healthy' : 'degraded'"
        />
        <AdminKpiCard
          :label="t('dashboard.kpi.entitlements')"
          :value="String(dashboard.entitlements.filter(entitlement => entitlement.internal).length)"
          :hint="t('dashboard.kpi.entitlementsHint')"
        />
        <AdminKpiCard
          :label="t('dashboard.kpi.audit')"
          :value="String(dashboard.recent_audit.length)"
          :hint="t('dashboard.kpi.auditHint')"
        />
      </div>

      <nav
        class="admin-quick-links"
        :aria-label="t('dashboard.links.title')"
      >
        <a
          v-for="link in quickLinks"
          :key="link.path"
          class="admin-quick-link"
          :href="link.href"
        >
          <span aria-hidden="true">{{ link.icon }}</span>
          <strong>{{ t(link.labelKey) }}</strong>
        </a>
      </nav>
    </template>
  </section>
</template>
