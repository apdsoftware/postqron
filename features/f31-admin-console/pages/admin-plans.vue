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
import AdminExportActions from '../components/AdminExportActions.vue'
import AdminPageHeader from '../components/AdminPageHeader.vue'
import AdminPagination from '../components/AdminPagination.vue'
import AdminPlansFilters from '../components/AdminPlansFilters.vue'
import AdminSortButton from '../components/AdminSortButton.vue'
import AdminTable from '../components/AdminTable.vue'
import { useAdminSectionLoad } from '../components/use-admin-section.ts'
import {
  AdminApiError,
  normalizeAdminApiError,
  type AdminPlanQuery,
} from '../core/api.ts'
import type {
  PlanRow,
  UsageSummary,
} from '../core/contracts.ts'
import {
  planQueryFromRoute,
  routeQuery,
} from '../core/list-query.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminPlanListState,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const api = useAdminApi()
const route = useRoute()
const router = useRouter()
const session = useAdminSessionState()
const plans = useAdminPlanListState()
const { date, t } = useAdminI18n()
const currentQuery = computed(() =>
  planQueryFromRoute(route.query as Readonly<Record<string, unknown>>))
const { loading, errorCode, reload } = useAdminSectionLoad(
  plans,
  () => api.plans(currentQuery.value),
)

watch(() => route.fullPath, reload)

useHead(computed(() => ({
  title: t('document.title'),
})))

const items = computed<readonly PlanRow[]>(() => plans.value?.items ?? [])
const page = computed({
  get: () => plans.value?.pagination.page ?? currentQuery.value.page ?? 1,
  set: (next: number) => updateQuery({ ...currentQuery.value, page: next }),
})
const total = computed(() => plans.value?.pagination.total ?? 0)
const csvHref = computed(() => api.plansExportURL(currentQuery.value, 'csv'))
const xlsxHref = computed(() => api.plansExportURL(currentQuery.value, 'xlsx'))

const saving = ref(false)
const success = ref(false)
const mutationError = ref<AdminApiError['code']>()
const confirmation = ref<{ close(): void, showModal(): void }>()
const selected = ref<PlanRow>()
const action = ref<'assign' | 'revoke'>('assign')
const reason = ref('')
const confirmed = ref(false)

const canSubmit = computed(() =>
  confirmed.value && reason.value.trim().length >= 8 && !saving.value)

const filterLabels = computed(() => ({
  search: t('filters.workspaceOwner'),
  searchPlaceholder: t('filters.workspaceOwnerPlaceholder'),
  plan: t('filters.plan'),
  status: t('filters.status'),
  type: t('filters.type'),
  from: t('filters.from'),
  to: t('filters.to'),
  timezone: t('filters.timezone'),
  all: t('filters.all'),
  public: t('entitlements.public'),
  internal: t('entitlements.internal'),
  trialing: t('status.trialing'),
  active: t('status.active'),
  pastDue: t('status.past_due'),
  trialExpired: t('status.trial_expired'),
  restricted: t('status.payment_restricted'),
  canceled: t('status.canceled'),
  reset: t('filters.reset'),
  apply: t('filters.apply'),
}))

async function updateQuery(query: AdminPlanQuery) {
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
    : field === 'created_at' || field === 'updated_at' ? 'desc' : 'asc'
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

function usageLabel(usage: UsageSummary): string {
  if (usage.unlimited) {
    return `${usage.used} / ${t('plans.unlimited')}`
  }
  return t('plans.usageValue', {
    used: usage.used,
    limit: usage.limit ?? 0,
    remaining: usage.remaining ?? 0,
  })
}

function openConfirmation(
  entitlement: PlanRow,
  nextAction: 'assign' | 'revoke',
) {
  selected.value = entitlement
  action.value = nextAction
  reason.value = ''
  confirmed.value = false
  success.value = false
  mutationError.value = undefined
  confirmation.value?.showModal()
}

function closeConfirmation() {
  if (!saving.value) {
    confirmation.value?.close()
  }
}

async function applyInternalPlan() {
  if (!selected.value || !session.value || !canSubmit.value) {
    mutationError.value = 'ADMIN_INVALID_REQUEST'
    return
  }
  saving.value = true
  mutationError.value = undefined
  try {
    await api.changeInternalPlan({
      action: action.value,
      workspaceId: selected.value.workspace_id,
      confirmed: confirmed.value,
      reason: reason.value.trim(),
      csrfToken: session.value.csrf_token,
      idempotencyKey: globalThis.crypto.randomUUID(),
    })
    success.value = true
    await reload()
  } catch (error) {
    mutationError.value = normalizeAdminApiError(error).code
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('plans.title')"
      :description="t('plans.description')"
    />

    <AdminPlansFilters
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
      <div class="admin-plans-desktop">
        <AdminTable
          :items="items"
          :get-key="(entitlement) => entitlement.workspace_id"
          :caption="t('plans.title')"
          :empty-message="t('status.empty')"
        >
          <template #head>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('plans.table.workspace')"
                :active="currentQuery.sort === 'workspace'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('workspace')"
              >
                {{ t('plans.table.workspace') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('plans.table.owner')"
                :active="currentQuery.sort === 'owner'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('owner')"
              >
                {{ t('plans.table.owner') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('plans.table.plan')"
                :active="currentQuery.sort === 'plan'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('plan')"
              >
                {{ t('plans.table.plan') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('plans.table.status')"
                :active="currentQuery.sort === 'status'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('status')"
              >
                {{ t('plans.table.status') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              {{ t('plans.table.usage') }}
            </th>
            <th scope="col">
              <AdminSortButton
                :label="sortLabel('plans.table.updated')"
                :active="currentQuery.sort === 'updated_at'"
                :direction="currentQuery.direction ?? 'desc'"
                @select="sort('updated_at')"
              >
                {{ t('plans.table.updated') }}
              </AdminSortButton>
            </th>
            <th scope="col">
              {{ t('plans.table.actions') }}
            </th>
          </template>
          <template #row="{ item }">
            <td>
              <strong>{{ item.workspace_name }}</strong>
              <small><code>{{ item.workspace_id }}</code></small>
            </td>
            <td>{{ item.owner_email }}</td>
            <td>
              <strong>{{ item.plan_code }}</strong>
              <small>{{ item.internal ? t('entitlements.internal') : t('entitlements.public') }}</small>
            </td>
            <td>{{ t(`status.${item.status}` as never) }}</td>
            <td>
              <small>{{ t('plans.usage.members') }}: {{ usageLabel(item.usage.members) }}</small>
              <small>{{ t('plans.usage.channels') }}: {{ usageLabel(item.usage.channels) }}</small>
              <small>{{ t('plans.usage.scheduled') }}: {{ usageLabel(item.usage.scheduled_publications) }}</small>
            </td>
            <td>
              <time :datetime="item.plan_updated_at">{{ date(item.plan_updated_at) }}</time>
              <small>{{ t('plans.periodEnd') }}: {{ date(item.period_end) }}</small>
            </td>
            <td>
              <button
                class="pq-button"
                :class="item.internal ? 'pq-button--secondary' : 'pq-button--primary'"
                type="button"
                @click="openConfirmation(item, item.internal ? 'revoke' : 'assign')"
              >
                {{ item.internal ? t('entitlements.revoke') : t('entitlements.assign') }}
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
          :key="item.workspace_id"
        >
          <div>
            <strong>{{ item.workspace_name }}</strong>
            <code>{{ item.workspace_id }}</code>
          </div>
          <dl>
            <dt>{{ t('plans.table.owner') }}</dt>
            <dd>{{ item.owner_email }}</dd>
            <dt>{{ t('plans.table.plan') }}</dt>
            <dd>{{ item.plan_code }} · {{ item.internal ? t('entitlements.internal') : t('entitlements.public') }}</dd>
            <dt>{{ t('plans.table.status') }}</dt>
            <dd>{{ t(`status.${item.status}` as never) }}</dd>
            <dt>{{ t('plans.usage.members') }}</dt>
            <dd>{{ usageLabel(item.usage.members) }}</dd>
            <dt>{{ t('plans.usage.channels') }}</dt>
            <dd>{{ usageLabel(item.usage.channels) }}</dd>
            <dt>{{ t('plans.usage.scheduled') }}</dt>
            <dd>{{ usageLabel(item.usage.scheduled_publications) }}</dd>
          </dl>
          <button
            class="pq-button pq-button--primary"
            type="button"
            @click="openConfirmation(item, item.internal ? 'revoke' : 'assign')"
          >
            {{ item.internal ? t('entitlements.revoke') : t('entitlements.assign') }}
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
        :page-size="plans?.pagination.page_size ?? 25"
        :previous-label="t('pagination.previous')"
        :next-label="t('pagination.next')"
        :status-label="(current, count) => t('pagination.status', { page: current, count })"
      />
    </template>

    <dialog
      ref="confirmation"
      class="admin-confirmation"
      @cancel.prevent="closeConfirmation"
    >
      <form @submit.prevent="applyInternalPlan">
        <h2>{{ t(action === 'assign' ? 'confirm.title.assign' : 'confirm.title.revoke') }}</h2>
        <p>{{ t('confirm.description') }}</p>
        <code>{{ selected?.workspace_id }}</code>
        <label for="admin-reason">{{ t('confirm.reason') }}</label>
        <textarea
          id="admin-reason"
          v-model="reason"
          minlength="8"
          maxlength="500"
          :placeholder="t('confirm.reasonPlaceholder')"
          required
        />
        <label class="admin-confirmation__check">
          <input
            v-model="confirmed"
            type="checkbox"
            required
          >
          <span>{{ t('confirm.checkbox') }}</span>
        </label>
        <p
          v-if="mutationError"
          class="admin-inline-error"
          role="alert"
        >
          {{ t(`error.${mutationError}` as never) }}
        </p>
        <p
          v-if="success"
          class="admin-inline-success"
          role="status"
        >
          {{ t('confirm.success') }}
        </p>
        <div class="admin-confirmation__actions">
          <button
            class="pq-button pq-button--secondary"
            type="button"
            :disabled="saving"
            @click="closeConfirmation"
          >
            {{ t('confirm.cancel') }}
          </button>
          <button
            class="pq-button pq-button--primary"
            type="submit"
            :disabled="!canSubmit"
          >
            {{ saving ? t('confirm.saving') : t('confirm.submit') }}
          </button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
:deep(.admin-table td) {
  vertical-align: top;
}

:deep(.admin-table td small) {
  display: block;
  margin-top: var(--pq-space-1);
  white-space: normal;
}

.admin-mobile-list {
  display: none;
}

.admin-mobile-empty {
  display: none;
}

@media (max-width: 48rem) {
  .admin-plans-desktop {
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
    display: grid;
    min-width: 0;
    gap: var(--pq-space-1);
  }

  .admin-mobile-list code,
  .admin-mobile-list dd {
    overflow-wrap: anywhere;
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
    margin: 0;
  }
}
</style>
