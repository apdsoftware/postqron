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
  UserDirectoryItem,
  UserDirectoryPage,
  UserDirectoryParams,
} from '../core/contracts.ts'
import {
  directoryRouteQuery,
  userDirectoryParamsFromQuery,
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

const result = ref<UserDirectoryPage>()
const loading = ref(true)
const exporting = ref(false)
const errorCode = ref<AdminApiError['code']>()
const exportErrorCode = ref<AdminApiError['code']>()
const detailErrorCode = ref<AdminApiError['code']>()
const detail = ref<UserDirectoryItem>()
const detailDialog = ref<{ close(): void, showModal(): void }>()

const query = ref('')
const status = ref('')
const verified = ref('')
const plan = ref('')
const loginMethod = ref('')
const registeredFrom = ref('')
const registeredTo = ref('')
const lastLoginFrom = ref('')
const lastLoginTo = ref('')
const page = ref(1)
const pageSize = ref(25)
const sort = ref('registered_at')
const direction = ref<DirectoryDirection>('desc')
let applyingRoute = false

useHead(computed(() => ({
  title: t('document.title'),
})))

const users = computed(() => result.value?.items ?? [])

function currentParameters(): UserDirectoryParams {
  return {
    ...(query.value.trim() ? { q: query.value.trim() } : {}),
    ...(status.value ? { status: status.value } : {}),
    ...(verified.value
      ? { email_verified: verified.value === 'true' }
      : {}),
    ...(plan.value ? { plan: plan.value } : {}),
    ...(loginMethod.value ? { login_method: loginMethod.value } : {}),
    ...(registeredFrom.value ? { registered_from: registeredFrom.value } : {}),
    ...(registeredTo.value ? { registered_to: registeredTo.value } : {}),
    ...(lastLoginFrom.value ? { last_login_from: lastLoginFrom.value } : {}),
    ...(lastLoginTo.value ? { last_login_to: lastLoginTo.value } : {}),
    page: page.value,
    page_size: pageSize.value,
    sort: sort.value,
    direction: direction.value,
  }
}

function applyRoute() {
  applyingRoute = true
  const parameters = userDirectoryParamsFromQuery(route.query)
  query.value = parameters.q ?? ''
  status.value = parameters.status ?? ''
  verified.value = parameters.email_verified === true
    ? 'true'
    : parameters.email_verified === false
      ? 'false'
      : ''
  plan.value = parameters.plan ?? ''
  loginMethod.value = parameters.login_method ?? ''
  registeredFrom.value = parameters.registered_from ?? ''
  registeredTo.value = parameters.registered_to ?? ''
  lastLoginFrom.value = parameters.last_login_from ?? ''
  lastLoginTo.value = parameters.last_login_to ?? ''
  page.value = parameters.page ?? 1
  pageSize.value = parameters.page_size ?? 25
  sort.value = parameters.sort ?? 'registered_at'
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
    result.value = await api.users(currentParameters())
  } catch (error) {
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    loading.value = false
  }
}

async function syncRoute(): Promise<boolean> {
  const target = directoryRouteQuery(currentParameters())
  const current = directoryRouteQuery(userDirectoryParamsFromQuery(route.query))
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
  verified.value = ''
  plan.value = ''
  loginMethod.value = ''
  registeredFrom.value = ''
  registeredTo.value = ''
  lastLoginFrom.value = ''
  lastLoginTo.value = ''
  page.value = 1
  sort.value = 'registered_at'
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

async function openDetail(accountId: string) {
  detail.value = undefined
  detailErrorCode.value = undefined
  detailDialog.value?.showModal()
  try {
    detail.value = await api.user(accountId)
  } catch (error) {
    detailErrorCode.value = normalizeAdminApiError(error).code
  }
}

function closeDetail() {
  detailDialog.value?.close()
}

function workspaceHref(id: string): string {
  const path = route.path.replace(/\/users$/u, '/workspaces')
  return `${path}?q=${encodeURIComponent(id)}`
}

async function exportDirectory(format: ExportFormat) {
  exporting.value = true
  exportErrorCode.value = undefined
  try {
    const download = await api.exportUsers(currentParameters(), format)
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
      :title="t('users.title')"
      :description="t('users.description')"
    />

    <form
      class="admin-directory-filters"
      @submit.prevent="applyFilters"
    >
      <div class="admin-directory-filters__quick">
        <label for="admin-users-search">{{ t('directory.search') }}</label>
        <input
          id="admin-users-search"
          v-model="query"
          type="search"
          minlength="2"
          maxlength="120"
          :placeholder="t('directory.searchPlaceholder')"
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
              <option value="locked">{{ t('directory.status.locked') }}</option>
            </select>
          </label>
          <label>
            <span>{{ t('users.filter.verified') }}</span>
            <select v-model="verified">
              <option value="">{{ t('directory.all') }}</option>
              <option value="true">{{ t('directory.yes') }}</option>
              <option value="false">{{ t('directory.no') }}</option>
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
            <span>{{ t('users.filter.loginMethod') }}</span>
            <select v-model="loginMethod">
              <option value="">{{ t('directory.all') }}</option>
              <option
                v-for="method in ['password', 'google', 'apple', 'facebook', 'linkedin']"
                :key="method"
                :value="method"
              >
                {{ method }}
              </option>
            </select>
          </label>
          <label>
            <span>{{ t('users.filter.registeredFrom') }}</span>
            <input
              v-model="registeredFrom"
              type="date"
            >
          </label>
          <label>
            <span>{{ t('users.filter.registeredTo') }}</span>
            <input
              v-model="registeredTo"
              type="date"
            >
          </label>
          <label>
            <span>{{ t('users.filter.lastLoginFrom') }}</span>
            <input
              v-model="lastLoginFrom"
              type="date"
            >
          </label>
          <label>
            <span>{{ t('users.filter.lastLoginTo') }}</span>
            <input
              v-model="lastLoginTo"
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
        :items="users"
        :get-key="user => user.id"
        :caption="t('users.title')"
        :empty-message="t('status.empty')"
        :aria-busy="loading"
      >
        <template #head>
          <th
            scope="col"
            :aria-sort="ariaSort('display_name')"
          >
            <button
              type="button"
              @click="sortBy('display_name')"
            >
              {{ t('users.table.name') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('email')"
          >
            <button
              type="button"
              @click="sortBy('email')"
            >
              {{ t('users.table.email') }}
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
              {{ t('users.table.status') }}
            </button>
          </th>
          <th scope="col">
            {{ t('users.table.methods') }}
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('registered_at')"
          >
            <button
              type="button"
              @click="sortBy('registered_at')"
            >
              {{ t('users.table.registered') }}
            </button>
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('last_login_at')"
          >
            <button
              type="button"
              @click="sortBy('last_login_at')"
            >
              {{ t('users.table.lastLogin') }}
            </button>
          </th>
          <th scope="col">
            {{ t('users.table.workspaces') }}
          </th>
          <th
            scope="col"
            :aria-sort="ariaSort('active_sessions')"
          >
            <button
              type="button"
              @click="sortBy('active_sessions')"
            >
              {{ t('users.table.sessions') }}
            </button>
          </th>
          <th scope="col">
            {{ t('directory.actions') }}
          </th>
        </template>
        <template #row="{ item }">
          <td>
            <strong>{{ item.display_name }}</strong>
          </td>
          <td>{{ item.email }}</td>
          <td>
            <span class="admin-directory-badge">{{ item.account_status }}</span>
            <small>
              {{ item.email_verified ? t('search.verified') : t('search.unverified') }}
            </small>
          </td>
          <td>{{ item.login_methods.join(', ') || t('directory.none') }}</td>
          <td>{{ date(item.registered_at) }}</td>
          <td>{{ item.last_login_at ? date(item.last_login_at) : t('directory.never') }}</td>
          <td>
            <ul v-if="item.workspaces.length">
              <li
                v-for="workspace in item.workspaces"
                :key="workspace.id"
              >
                <a :href="workspaceHref(workspace.id)">{{ workspace.name }}</a>
                <small>{{ workspace.role }} · {{ workspace.plan_code }} · {{ workspace.plan_status }}</small>
              </li>
            </ul>
            <span v-else>{{ t('directory.none') }}</span>
          </td>
          <td>{{ item.active_sessions }}</td>
          <td>
            <button
              class="pq-button pq-button--secondary"
              type="button"
              @click="openDetail(item.id)"
            >
              {{ t('users.detail.open') }}
            </button>
          </td>
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

    <dialog
      ref="detailDialog"
      class="admin-user-detail"
      @cancel.prevent="closeDetail"
    >
      <section aria-labelledby="admin-user-detail-title">
        <h2 id="admin-user-detail-title">
          {{ detail?.display_name ?? t('users.detail.title') }}
        </h2>
        <AdminAlert
          v-if="detailErrorCode"
          variant="error"
        >
          {{ t(`error.${detailErrorCode}` as never) }}
        </AdminAlert>
        <p
          v-else-if="!detail"
          role="status"
        >
          {{ t('status.loading') }}
        </p>
        <dl v-else>
          <div>
            <dt>{{ t('users.table.email') }}</dt>
            <dd>{{ detail.email }}</dd>
          </div>
          <div>
            <dt>{{ t('users.table.status') }}</dt>
            <dd>{{ detail.account_status }}</dd>
          </div>
          <div>
            <dt>{{ t('users.table.methods') }}</dt>
            <dd>{{ detail.login_methods.join(', ') || t('directory.none') }}</dd>
          </div>
          <div>
            <dt>{{ t('users.table.workspaces') }}</dt>
            <dd>{{ detail.workspaces.length }}</dd>
          </div>
          <div>
            <dt>{{ t('users.table.sessions') }}</dt>
            <dd>{{ detail.active_sessions }}</dd>
          </div>
        </dl>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          @click="closeDetail"
        >
          {{ t('directory.close') }}
        </button>
      </section>
    </dialog>
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

:deep(.admin-table td ul) {
  display: grid;
  gap: .4rem;
  list-style: none;
  margin: 0;
  padding: 0;
}

.admin-directory-badge {
  background: #edf2ff;
  border-radius: 999px;
  display: inline-block;
  font-size: .8rem;
  font-weight: 700;
  padding: .15rem .5rem;
}

.admin-user-detail {
  border: 0;
  border-radius: .85rem;
  box-shadow: 0 1.5rem 4rem rgb(15 23 42 / 24%);
  max-width: min(34rem, calc(100vw - 2rem));
  width: 100%;
}

.admin-user-detail::backdrop {
  background: rgb(15 23 42 / 58%);
}

.admin-user-detail section,
.admin-user-detail dl {
  display: grid;
  gap: 1rem;
}

.admin-user-detail dl div {
  border-bottom: 1px solid var(--pq-color-border, #d9dee8);
  display: grid;
  gap: .25rem;
  padding-bottom: .65rem;
}

.admin-user-detail dt {
  font-weight: 700;
}

.admin-user-detail dd {
  margin: 0;
}

@media (max-width: 620px) {
  .admin-directory-filters__quick {
    grid-template-columns: 1fr;
  }
}
</style>
