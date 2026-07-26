<script setup lang="ts">
import { computed, definePageMeta, ref, useHead } from '#imports'
import { AdminApiError, normalizeAdminApiError } from '../core/api.ts'
import {
  useAdminApi,
  useAdminDashboardState,
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const api = useAdminApi()
const session = useAdminSessionState()
const dashboard = useAdminDashboardState()
const { date, t } = useAdminI18n()

useHead(computed(() => ({
  title: `${t('audit.page.title')} — Postqron`,
})))

const loading = ref(true)
const errorCode = ref<AdminApiError['code']>()

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
      :eyebrow="t('audit.page.eyebrow')"
      :title="t('audit.page.title')"
      :description="t('audit.page.description')"
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

    <section
      v-else-if="dashboard"
      class="admin-panel"
      aria-labelledby="admin-audit-title"
    >
      <h2 id="admin-audit-title">
        {{ t('audit.title') }}
      </h2>
      <AdminDataTable
        :caption="t('audit.title')"
        :columns="[
          { key: 'event', label: t('audit.table.event') },
          { key: 'outcome', label: t('audit.table.outcome') },
          { key: 'reason', label: t('audit.table.reason') },
          { key: 'when', label: t('audit.table.when') },
        ]"
        :rows="dashboard.recent_audit.map(event => ({
          event: event.code,
          outcome: event.outcome,
          reason: event.reason,
          when: date(event.occurred_at),
        }))"
        :empty-message="t('status.empty')"
      />
    </section>
  </section>
</template>
