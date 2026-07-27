<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useHead,
  useRoute,
  useRouter,
  watch,
} from '#imports'
import AdminAlert from '../components/AdminAlert.vue'
import AdminAuditFilters from '../components/AdminAuditFilters.vue'
import AdminExportActions from '../components/AdminExportActions.vue'
import AdminPageHeader from '../components/AdminPageHeader.vue'
import AdminPagination from '../components/AdminPagination.vue'
import AdminSortButton from '../components/AdminSortButton.vue'
import AdminTable from '../components/AdminTable.vue'
import { useAdminSectionLoad } from '../components/use-admin-section.ts'
import {
  normalizeAdminApiError,
  type AdminApiError,
  type AdminAuditQuery,
} from '../core/api.ts'
import type { AuditEvent } from '../core/contracts.ts'
import {
  auditQueryFromRoute,
  routeQuery,
} from '../core/list-query.ts'
import {
  useAdminApi,
  useAdminAuditListState,
  useAdminI18n,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const api = useAdminApi()
const route = useRoute()
const router = useRouter()
const audit = useAdminAuditListState()
const { date, t } = useAdminI18n()
const currentQuery = computed(() =>
  auditQueryFromRoute(route.query as Readonly<Record<string, unknown>>))
const { loading, errorCode, reload } = useAdminSectionLoad(
  audit,
  () => api.audit(currentQuery.value),
)

watch(() => route.fullPath, reload)

useHead(computed(() => ({
  title: t('document.title'),
})))

const items = computed<readonly AuditEvent[]>(() => audit.value?.items ?? [])
const page = computed({
  get: () => audit.value?.pagination.page ?? currentQuery.value.page ?? 1,
  set: (next: number) => updateQuery({ ...currentQuery.value, page: next }),
})
const total = computed(() => audit.value?.pagination.total ?? 0)
const csvHref = computed(() => api.auditExportURL(currentQuery.value, 'csv'))
const xlsxHref = computed(() => api.auditExportURL(currentQuery.value, 'xlsx'))

const detailDialog = ref<{ close(): void, showModal(): void }>()
const detail = ref<AuditEvent>()
const detailLoading = ref(false)
const detailError = ref<AdminApiError['code']>()

const filterLabels = computed(() => ({
  action: t('audit.filters.action'),
  actionPlaceholder: t('audit.filters.actionPlaceholder'),
  actor: t('audit.filters.actor'),
  actorPlaceholder: t('audit.filters.actorPlaceholder'),
  subject: t('audit.filters.subject'),
  subjectPlaceholder: t('audit.filters.subjectPlaceholder'),
  outcome: t('audit.filters.outcome'),
  outcomePlaceholder: t('audit.filters.outcomePlaceholder'),
  from: t('filters.from'),
  to: t('filters.to'),
  timezone: t('filters.timezone'),
  reset: t('filters.reset'),
  apply: t('filters.apply'),
}))

async function updateQuery(query: AdminAuditQuery) {
  await router.push({
    query: routeQuery({ ...query }),
  })
}

async function resetFilters() {
  await router.push({ query: {} })
}

async function sort(field: string) {
  const direction = currentQuery.value.sort === field
    ? currentQuery.value.direction === 'asc' ? 'desc' : 'asc'
    : field === 'occurred_at' ? 'desc' : 'asc'
  await updateQuery({
    ...currentQuery.value,
    sort: field,
    direction,
    page: 1,
  })
}

function sortLabel(key: string): string {
  return t('sort.label', { field: t(key as never) })
}

async function openDetail(event: AuditEvent) {
  detail.value = undefined
  detailError.value = undefined
  detailLoading.value = true
  detailDialog.value?.showModal()
  try {
    detail.value = await api.auditEvent(event.id)
  } catch (error) {
    detailError.value = normalizeAdminApiError(error).code
  } finally {
    detailLoading.value = false
  }
}
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('audit.title')"
      :description="t('audit.description')"
    />

    <AdminAuditFilters
      :query="currentQuery"
      :labels="filterLabels"
      :disabled="loading"
      @apply="updateQuery"
      @reset="resetFilters"
    />

    <AdminExportActions
      :label="t('export.label')"
      :csv-label="t('export.csv')"
      :xlsx-label="t('export.xlsx')"
      :limit-label="t('export.limit', { limit: 10000 })"
      :csv-href="csvHref"
      :xlsx-href="xlsxHref"
    />

    <AdminAlert
      v-if="loading"
      variant="info"
    >
      {{ t('status.loading') }}
    </AdminAlert>
    <AdminAlert
      v-else-if="errorCode"
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
      <div class="admin-audit-desktop">
        <AdminTable
          :items="items"
          :get-key="(event) => event.id"
          :caption="t('audit.title')"
          :empty-message="t('status.empty')"
        >
          <template #head>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('audit.table.occurredAt')"
                :active="currentQuery.sort === 'occurred_at'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('occurred_at')"
              >
                {{ t('audit.table.occurredAt') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('audit.table.event')"
                :active="currentQuery.sort === 'code'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('code')"
              >
                {{ t('audit.table.event') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('audit.table.actor')"
                :active="currentQuery.sort === 'actor'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('actor')"
              >
                {{ t('audit.table.actor') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('audit.table.subject')"
                :active="currentQuery.sort === 'subject'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('subject')"
              >
                {{ t('audit.table.subject') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('audit.outcome')"
                :active="currentQuery.sort === 'outcome'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('outcome')"
              >
                {{ t('audit.outcome') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              {{ t('audit.reason') }}
            </th>
            <th scope="col">
              {{ t('audit.table.correlation') }}
            </th>
            <th scope="col">
              {{ t('audit.table.details') }}
            </th>
          </template>
          <template #row="{ item }">
            <td>
              <time :datetime="item.occurred_at">{{ date(item.occurred_at) }}</time>
            </td>
            <td><code>{{ item.code }}</code></td>
            <td><code>{{ item.actor_id }}</code></td>
            <td><code>{{ item.subject_id }}</code></td>
            <td>{{ item.outcome }}</td>
            <td class="admin-audit-reason">
              {{ item.reason }}
            </td>
            <td><small>{{ item.correlation_id }}</small></td>
            <td>
              <button
                class="pq-button pq-button--secondary"
                type="button"
                @click="openDetail(item)"
              >
                {{ t('audit.details.open') }}
              </button>
            </td>
          </template>
        </AdminTable>
      </div>

      <ul
        v-if="items.length"
        class="admin-mobile-list"
      >
        <li
          v-for="item in items"
          :key="item.id"
        >
          <div>
            <code>{{ item.code }}</code>
            <time :datetime="item.occurred_at">{{ date(item.occurred_at) }}</time>
          </div>
          <dl>
            <dt>{{ t('audit.table.actor') }}</dt>
            <dd><code>{{ item.actor_id }}</code></dd>
            <dt>{{ t('audit.table.subject') }}</dt>
            <dd><code>{{ item.subject_id }}</code></dd>
            <dt>{{ t('audit.outcome') }}</dt>
            <dd>{{ item.outcome }}</dd>
            <dt>{{ t('audit.reason') }}</dt>
            <dd>{{ item.reason }}</dd>
            <dt>{{ t('audit.table.correlation') }}</dt>
            <dd>{{ item.correlation_id }}</dd>
          </dl>
          <button
            class="pq-button pq-button--secondary"
            type="button"
            @click="openDetail(item)"
          >
            {{ t('audit.details.open') }}
          </button>
        </li>
      </ul>
      <p
        v-else
        class="admin-mobile-empty admin-state"
        role="status"
      >
        {{ t('status.empty') }}
      </p>

      <AdminPagination
        v-model:page="page"
        :total="total"
        :page-size="audit?.pagination.page_size ?? 25"
        :previous-label="t('pagination.previous')"
        :next-label="t('pagination.next')"
        :status-label="(current, count) => t('pagination.status', { page: current, count })"
      />
    </template>

    <dialog
      ref="detailDialog"
      class="admin-confirmation admin-audit-detail"
    >
      <div>
        <h2>{{ t('audit.details.title') }}</h2>
        <AdminAlert
          v-if="detailLoading"
          variant="info"
        >
          {{ t('status.loading') }}
        </AdminAlert>
        <AdminAlert
          v-else-if="detailError"
          variant="error"
        >
          {{ t(`error.${detailError}` as never) }}
        </AdminAlert>
        <dl v-else-if="detail">
          <dt>{{ t('audit.details.id') }}</dt>
          <dd><code>{{ detail.id }}</code></dd>
          <dt>{{ t('audit.table.occurredAt') }}</dt>
          <dd><time :datetime="detail.occurred_at">{{ date(detail.occurred_at) }}</time></dd>
          <dt>{{ t('audit.table.event') }}</dt>
          <dd><code>{{ detail.code }}</code></dd>
          <dt>{{ t('audit.table.actor') }}</dt>
          <dd><code>{{ detail.actor_id }}</code></dd>
          <dt>{{ t('audit.table.subject') }}</dt>
          <dd><code>{{ detail.subject_id }}</code></dd>
          <dt>{{ t('audit.outcome') }}</dt>
          <dd>{{ detail.outcome }}</dd>
          <dt>{{ t('audit.reason') }}</dt>
          <dd>{{ detail.reason }}</dd>
          <dt>{{ t('audit.table.correlation') }}</dt>
          <dd><code>{{ detail.correlation_id }}</code></dd>
        </dl>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          @click="detailDialog?.close()"
        >
          {{ t('audit.details.close') }}
        </button>
      </div>
    </dialog>
  </section>
</template>

<style scoped>
:deep(.admin-table td) {
  vertical-align: top;
}

.admin-audit-reason {
  max-width: 22rem;
  white-space: normal;
}

.admin-mobile-list {
  display: none;
}

.admin-mobile-empty {
  display: none;
}

.admin-audit-detail > div {
  display: grid;
  padding: clamp(1.25rem, 4vw, 2rem);
  gap: var(--pq-space-4);
}

.admin-audit-detail h2 {
  margin: 0;
}

.admin-audit-detail dl {
  display: grid;
  margin: 0;
  grid-template-columns: minmax(7rem, auto) minmax(0, 1fr);
  gap: var(--pq-space-2) var(--pq-space-4);
}

.admin-audit-detail dt {
  color: var(--pq-color-text-muted);
  font-weight: var(--pq-font-weight-semibold);
}

.admin-audit-detail dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

@media (max-width: 48rem) {
  .admin-audit-desktop {
    display: none;
  }

  .admin-mobile-list {
    display: grid;
    margin: 0;
    padding: 0;
    gap: var(--pq-space-3);
    list-style: none;
  }

  .admin-mobile-empty {
    display: block;
  }

  .admin-mobile-list li {
    display: grid;
    min-width: 0;
    gap: var(--pq-space-3);
    border: 1px solid var(--pq-color-border);
    border-radius: var(--pq-radius-lg);
    padding: var(--pq-space-4);
    background: var(--pq-color-surface);
  }

  .admin-mobile-list li > div {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--pq-space-2);
  }

  .admin-mobile-list dl {
    display: grid;
    margin: 0;
    grid-template-columns: minmax(6rem, auto) minmax(0, 1fr);
    gap: var(--pq-space-2);
  }

  .admin-mobile-list dt {
    color: var(--pq-color-text-muted);
    font-weight: var(--pq-font-weight-semibold);
  }

  .admin-mobile-list dd {
    min-width: 0;
    margin: 0;
    overflow-wrap: anywhere;
  }

  .admin-audit-detail dl {
    grid-template-columns: 1fr;
  }
}
</style>
