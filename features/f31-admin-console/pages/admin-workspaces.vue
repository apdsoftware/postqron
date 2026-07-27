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
import AdminSearchFilter from '../components/AdminSearchFilter.vue'
import AdminTable from '../components/AdminTable.vue'
import { paginate } from '../components/paginate.ts'
import { AdminApiError, normalizeAdminApiError } from '../core/api.ts'
import type { WorkspaceSummary } from '../core/contracts.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminSearchState,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const PAGE_SIZE = 10

const api = useAdminApi()
const session = useAdminSessionState()
const searchResults = useAdminSearchState()
searchResults.value = undefined
const { t } = useAdminI18n()
const query = ref('')
const searching = ref(false)
const errorCode = ref<AdminApiError['code']>()
const page = ref(1)

useHead(computed(() => ({
  title: t('document.title'),
})))

const workspaces = computed<readonly WorkspaceSummary[]>(() => searchResults.value?.workspaces ?? [])
const pageItems = computed(() => paginate(workspaces.value, page.value, PAGE_SIZE))

async function search() {
  if (!session.value) {
    return
  }
  searching.value = true
  errorCode.value = undefined
  page.value = 1
  try {
    searchResults.value = await api.search(query.value)
  } catch (error) {
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    searching.value = false
  }
}
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('workspaces.title')"
      :description="t('workspaces.description')"
    />

    <AdminSearchFilter
      v-model:query="query"
      input-id="admin-workspaces-search"
      :label="t('search.label')"
      :placeholder="t('search.placeholder')"
      :submit-label="t('search.submit')"
      :disabled="searching"
      @submit="search"
    />

    <AdminAlert
      v-if="errorCode"
      variant="error"
    >
      {{ t(`error.${errorCode}` as never) }}
    </AdminAlert>

    <AdminTable
      :items="pageItems"
      :get-key="(workspace) => workspace.id"
      :caption="t('workspaces.title')"
      :empty-message="t('status.empty')"
    >
      <template #head>
        <th scope="col">
          {{ t('workspaces.table.name') }}
        </th>
        <th scope="col">
          {{ t('workspaces.table.owner') }}
        </th>
        <th scope="col">
          {{ t('workspaces.table.members') }}
        </th>
      </template>
      <template #row="{ item }">
        <td>{{ item.name }}</td>
        <td>{{ item.owner_email }}</td>
        <td>{{ item.member_count }}</td>
      </template>
    </AdminTable>

    <AdminPagination
      v-model:page="page"
      :total="workspaces.length"
      :page-size="PAGE_SIZE"
      :previous-label="t('pagination.previous')"
      :next-label="t('pagination.next')"
      :status-label="(current, count) => t('pagination.status', { page: current, count })"
    />
  </section>
</template>
