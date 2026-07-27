<script setup lang="ts">
import {
  computed,
  definePageMeta,
  nextTick,
  ref,
  useHead,
  useRoute,
  useRouter,
  watch,
} from '#imports'
import AdminAlert from '../components/AdminAlert.vue'
import AdminDirectoryExport from '../components/AdminDirectoryExport.vue'
import AdminDirectoryPagination from '../components/AdminDirectoryPagination.vue'
import AdminPageHeader from '../components/AdminPageHeader.vue'
import AdminTable from '../components/AdminTable.vue'
import { normalizeAdminApiError, type AdminApiError } from '../core/api.ts'
import type {
  DirectoryDirection,
  ExportFormat,
  WorkspaceDirectoryPage,
  WorkspaceDirectoryParams,
} from '../core/contracts.ts'
import {
  directoryRouteQuery,
  workspaceDirectoryParamsFromQuery,
} from '../core/directory-query.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const api = useAdminApi()
const session = useAdminSessionState()
const route = useRoute()
const router = useRouter()
const { date, t } = useAdminI18n()

const result = ref<WorkspaceDirectoryPage>()
const loading = ref(true)
const exporting = ref(false)
const errorCode = ref<AdminApiError['code']>()
const exportErrorCode = ref<AdminApiError['code']>()

const query = ref('')
const status = ref('')
const plan = ref('')
const owner = ref('')
const createdFrom = ref('')
const createdTo = ref('')
const updatedFrom = ref('')
const updatedTo = ref('')
const page = ref(1)
const pageSize = ref(25)
const sort = ref('updated_at')
const direction = ref<DirectoryDirection>('desc')
let applyingRoute = false

useHead(computed(() => ({
  title: t('document.title'),
})))

const workspaces = computed(() => result.value?.items ?? [])

function currentParameters(): WorkspaceDirectoryParams {
  return {
    ...(query.value.trim() ? { q: query.value.trim() } : {}),
    ...(status.value ? { status: status.value } : {}),
    ...(plan.value ? { plan: plan.value } : {}),
    ...(owner.value.trim() ? { owner: owner.value.trim() } : {}),
    ...(createdFrom.value ? { created_from: createdFrom.value } : {}),
    ...(createdTo.value ? { created_to: createdTo.value } : {}),
    ...(updatedFrom.value ? { updated_from: updatedFrom.value } : {}),
    ...(updatedTo.value ? { updated_to: updatedTo.value } : {}),
    page: page.value,
    page_size: pageSize.value,
    sort: sort.value,
    direction: direction.value,
  }
}

function applyRoute() {
  applyingRoute = true
  const parameters = workspaceDirectoryParamsFromQuery(route.query)
  query.value = parameters.q ?? ''
  status.value = parameters.status ?? ''
  plan.value = parameters.plan ?? ''
  owner.value = parameters.owner ?? ''
  createdFrom.value = parameters.created_from ?? ''
  createdTo.value = parameters.created_to ?? ''
  updatedFrom.value = parameters.updated_from ?? ''
  updatedTo.value = parameters.updated_to ?? ''
  page.value = parameters.page ?? 1
  pageSize.value = parameters.page_size ?? 25
  sort.value = parameters.sort ?? 'updated_at'
  direction.value = parameters.direction ?? 'desc'
  void nextTick(() => {
    applyingRoute = false
  })
}

async function load() {
  if (!import.meta.client || !session.value) {
    loading.value = false
    return
  }
  loading.value = true
  errorCode.value = undefined
  try {
    result.value = await api.workspaces(currentParameters())
  } catch (error) {
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    loading.value = false
  }
}

async function syncRoute(): Promise<boolean> {
  const target = directoryRouteQuery(currentParameters())
  const current = directoryRouteQuery(
    workspaceDirectoryParamsFromQuery(route.query),
  )
  if (
    new globalThis.URLSearchParams(target).toString()
    === new globalThis.URLSearchParams(current).toString()
  ) {
    return false
  }
  await router.replace({ query: target })
  return true
}

async function applyFilters() {
  page.value = 1
  if (!await syncRoute()) {
    await load()
  }
}

async function resetFilters() {
  query.value = ''
  status.value = ''
  plan.value = ''
  owner.value = ''
  createdFrom.value = ''
  createdTo.value = ''
  updatedFrom.value = ''
  updatedTo.value = ''
  page.value = 1
  sort.value = 'updated_at'
  direction.value = 'desc'
  if (!await syncRoute()) {
    await load()
  }
}

async function sortBy(column: string) {
  if (sort.value === column) {
    direction.value = direction.value === 'asc' ? 'desc' : 'asc'
  } else {
    sort.value = column
    direction.value = 'asc'
  }
  page.value = 1
  if (!await syncRoute()) {
    await load()
  }
}

function ariaSort(column: string): 'ascending' | 'descending' | 'none' {
  return sort.value === column
    ? direction.value === 'asc' ? 'ascending' : 'descending'
    : 'none'
}

function usersHref(email: string): string {
  const path = route.path.replace(/\/workspaces$/u, '/users')
  return `${path}?q=${encodeURIComponent(email)}`
}

function plansHref(): string {
  return route.path.replace(/\/workspaces$/u, '/plans')
}

async function exportDirectory(format: ExportFormat) {
  exporting.value = true
  exportErrorCode.value = undefined
  try {
    const download = await api.exportWorkspaces(currentParameters(), format)
    const href = globalThis.URL.createObjectURL(download.body)
    const anchor = globalThis.document.createElement('a')
    anchor.href = href
    anchor.download = download.filename
    anchor.click()
    await nextTick()
    globalThis.URL.revokeObjectURL(href)
  } catch (error) {
    exportErrorCode.value = normalizeAdminApiError(error).code
  } finally {
    exporting.value = false
  }
}

watch(
  () => route.query,
  async () => {
    applyRoute()
    await load()
  },
  { deep: true, immediate: true },
)

watch(session, load)
watch([page, pageSize], async () => {
  if (!applyingRoute) {
    if (!await syncRoute()) {
      await load()
    }
  }
})
</script>

<template>
  <section class="admin-page admin-directory-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('workspaces.title')"
      :description="t('workspaces.description')"
    />

    <form
      class="admin-directory-filters"
      @submit.prevent="applyFilters"
    >
      <div class="admin-directory-filters__quick">
        <label for="admin-workspaces-search">{{ t('directory.search') }}</label>
        <input
          id="admin-workspaces-search"
          v-model="query"
          type="search"
          minlength="2"
          maxlength="120"
          :placeholder="t('workspaces.searchPlaceholder')"
        >
        <button
          class="pq-button pq-button--primary"
          type="submit"
          :disabled="loading"
        >
          {{ t('directory.apply') }}
        </button>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          :disabled="loading"
          @click="resetFilters"
        >
          {{ t('directory.reset') }}
        </button>
      </div>

      <details>
        <summary>{{ t('directory.advanced') }}</summary>
        <div class="admin-directory-filters__grid">
          <label>
            <span>{{ t('directory.status') }}</span>
            <select v-model="status">
              <option value="">{{ t('directory.all') }}</option>
              <option value="active">{{ t('directory.status.active') }}</option>
              <option value="deletion_pending">{{ t('directory.status.deletionPending') }}</option>
            </select>
          </label>
          <label>
            <span>{{ t('directory.plan') }}</span>
            <select v-model="plan">
              <option value="">{{ t('directory.all') }}</option>
              <option
                v-for="code in ['start', 'pro', 'team', 'internal']"
                :key="code"
                :value="code"
              >
                {{ code }}
              </option>
            </select>
          </label>
          <label>
            <span>{{ t('workspaces.filter.owner') }}</span>
            <input
              v-model="owner"
              type="search"
              maxlength="120"
            >
          </label>
          <label>
            <span>{{ t('workspaces.filter.createdFrom') }}</span>
            <input
              v-model="createdFrom"
              type="date"
            >
          </label>
          <label>
            <span>{{ t('workspaces.filter.createdTo') }}</span>
            <input
              v-model="createdTo"
              type="date"
            >
          </label>
          <label>
            <span>{{ t('workspaces.filter.updatedFrom') }}</span>
            <input
              v-model="updatedFrom"
              type="date"
            >
          </label>
          <label>
            <span>{{ t('workspaces.filter.updatedTo') }}</span>
            <input
              v-model="updatedTo"
              type="date"
            >
          </label>
        </div>
      </details>
    </form>

    <AdminAlert
      v-if="loading && !result"
      variant="info"
    >
      {{ t('status.loading') }}
    </AdminAlert>
    <AdminAlert
      v-else-if="errorCode && !result"
      variant="error"
    >
      {{ t(`error.${errorCode}` as never) }}
      <button
        class="pq-button pq-button--secondary"
        type="button"
        @click="load"
      >
        {{ t('status.retry') }}
      </button>
    </AdminAlert>

    <template v-else>
      <div
        class="admin-directory-summary"
        aria-live="polite"
      >
        {{ t('directory.results', { total: result?.total ?? 0 }) }}
      </div>
      <AdminTable
        :items="workspaces"
        :get-key="workspace => workspace.id"
        :caption="t('workspaces.title')"
        :empty-message="t('status.empty')"
        :aria-busy="loading"
      >
        <template #head>
          <th
            scope="col"
            :aria-sort="ariaSort('name')"
          >
            <button
              type="button"
              @click="sortBy('name')"
            >
              {{ t('workspaces.table.name') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('owner_email')"
          >
            <button
              type="button"
              @click="sortBy('owner_email')"
            >
              {{ t('workspaces.table.owner') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('status')"
          >
            <button
              type="button"
              @click="sortBy('status')"
            >
              {{ t('workspaces.table.status') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('plan_code')"
          >
            <button
              type="button"
              @click="sortBy('plan_code')"
            >
              {{ t('workspaces.table.plan') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('member_count')"
          >
            <button
              type="button"
              @click="sortBy('member_count')"
            >
              {{ t('workspaces.table.members') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('channel_count')"
          >
            <button
              type="button"
              @click="sortBy('channel_count')"
            >
              {{ t('workspaces.table.channels') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('post_count')"
          >
            <button
              type="button"
              @click="sortBy('post_count')"
            >
              {{ t('workspaces.table.posts') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('created_at')"
          >
            <button
              type="button"
              @click="sortBy('created_at')"
            >
              {{ t('workspaces.table.created') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('updated_at')"
          >
            <button
              type="button"
              @click="sortBy('updated_at')"
            >
              {{ t('workspaces.table.updated') }}
            </button>
          </th>
        </template>
        <template #row="{ item }">
          <td>
            <strong>{{ item.name }}</strong>
            <small>{{ item.id }}</small>
          </td>
          <td>
            <a :href="usersHref(item.owner_email)">{{ item.owner_display_name }}</a>
            <small>{{ item.owner_email }}</small>
          </td>
          <td><span class="admin-directory-badge">{{ item.status }}</span></td>
          <td>
            <a :href="plansHref()">{{ item.plan_code }}</a>
            <small>{{ item.plan_status }}</small>
          </td>
          <td>{{ item.member_count }}</td>
          <td>{{ item.channel_count }}</td>
          <td>{{ item.post_count }}</td>
          <td>{{ date(item.created_at) }}</td>
          <td>{{ date(item.updated_at) }}</td>
        </template>
      </AdminTable>

      <AdminDirectoryPagination
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="result?.total ?? 0"
        :disabled="loading"
        :previous-label="t('pagination.previous')"
        :next-label="t('pagination.next')"
        :page-size-label="t('directory.pageSize')"
        :status-label="(current, count, total) => t('directory.pageStatus', { page: current, count, total })"
      />

      <AdminAlert
        v-if="exportErrorCode"
        variant="error"
      >
        {{ t(`error.${exportErrorCode}` as never) }}
      </AdminAlert>
      <AdminDirectoryExport
        :disabled="exporting || loading"
        :csv-label="t('directory.export.csv')"
        :xlsx-label="t('directory.export.xlsx')"
        :limit-label="t('directory.export.limit', { limit: 10000 })"
        @export="exportDirectory"
      />
    </template>
  </section>
</template>

<style scoped>
.admin-directory-page {
  display: grid;
  gap: 1rem;
}

.admin-directory-filters {
  background: var(--pq-color-surface, #fff);
  border: 1px solid var(--pq-color-border, #d9dee8);
  border-radius: .75rem;
  display: grid;
  gap: 1rem;
  padding: 1rem;
}

.admin-directory-filters__quick {
  align-items: end;
  display: grid;
  gap: .65rem;
  grid-template-columns: minmax(12rem, 1fr) auto auto;
}

.admin-directory-filters__quick label {
  grid-column: 1 / -1;
}

.admin-directory-filters input,
.admin-directory-filters select {
  min-height: 2.75rem;
  width: 100%;
}

.admin-directory-filters summary {
  cursor: pointer;
  font-weight: 700;
}

.admin-directory-filters__grid {
  display: grid;
  gap: .75rem;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  padding-top: 1rem;
}

.admin-directory-filters__grid label {
  display: grid;
  gap: .35rem;
}

.admin-directory-summary {
  font-weight: 700;
}

:deep(.admin-table th button) {
  background: transparent;
  border: 0;
  color: inherit;
  cursor: pointer;
  font: inherit;
  padding: 0;
  text-align: left;
  text-decoration: underline;
  text-underline-offset: .2em;
}

:deep(.admin-table td strong),
:deep(.admin-table td small) {
  display: block;
}

.admin-directory-badge {
  background: #edf2ff;
  border-radius: 999px;
  display: inline-block;
  font-size: .8rem;
  font-weight: 700;
  padding: .15rem .5rem;
}

@media (max-width: 620px) {
  .admin-directory-filters__quick {
    grid-template-columns: 1fr;
  }
}
</style>
