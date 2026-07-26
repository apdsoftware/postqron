<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useHead,
} from '#imports'
import AdminAlert from '../components/AdminAlert.vue'
import AdminPageHeader from '../components/AdminPageHeader.vue'
import AdminPagination from '../components/AdminPagination.vue'
import AdminTable from '../components/AdminTable.vue'
import { paginate } from '../components/paginate.ts'
import { useAdminSectionLoad } from '../components/use-admin-section.ts'
import type { AuditEvent } from '../core/contracts.ts'
import {
  useAdminApi,
  useAdminDashboardState,
  useAdminI18n,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const PAGE_SIZE = 10

const api = useAdminApi()
const dashboard = useAdminDashboardState()
const { date, t } = useAdminI18n()
const { loading, errorCode, reload } = useAdminSectionLoad(
  dashboard,
  () => api.dashboard(),
)
const page = ref(1)

useHead(computed(() => ({
  title: t('document.title'),
})))

const auditEvents = computed<readonly AuditEvent[]>(() => dashboard.value?.recent_audit ?? [])
const pageItems = computed(() => paginate(auditEvents.value, page.value, PAGE_SIZE))
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('audit.title')"
      :description="t('audit.description')"
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

    <template v-else>
      <AdminTable
        :items="pageItems"
        :get-key="(event) => event.id"
        :caption="t('audit.title')"
        :empty-message="t('status.empty')"
      >
        <template #head>
          <th scope="col">
            {{ t('audit.table.event') }}
          </th>
          <th scope="col">
            {{ t('audit.table.occurredAt') }}
          </th>
          <th scope="col">
            {{ t('audit.outcome') }}
          </th>
          <th scope="col">
            {{ t('audit.reason') }}
          </th>
          <th scope="col">
            {{ t('audit.table.correlation') }}
          </th>
        </template>
        <template #row="{ item }">
          <td>
            <code>{{ item.code }}</code>
          </td>
          <td>
            <time :datetime="item.occurred_at">{{ date(item.occurred_at) }}</time>
          </td>
          <td>{{ item.outcome }}</td>
          <td>{{ item.reason }}</td>
          <td>
            <small>{{ item.correlation_id }}</small>
          </td>
        </template>
      </AdminTable>

      <AdminPagination
        v-model:page="page"
        :total="auditEvents.length"
        :page-size="PAGE_SIZE"
        :previous-label="t('pagination.previous')"
        :next-label="t('pagination.next')"
        :status-label="(current, count) => t('pagination.status', { page: current, count })"
      />
    </template>
  </section>
</template>
