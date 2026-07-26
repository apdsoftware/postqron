<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useHead,
} from '#imports'
import AdminAlert from '../components/AdminAlert.vue'
import AdminPageHeader from '../components/AdminPageHeader.vue'
import AdminTable from '../components/AdminTable.vue'
import { useAdminSectionLoad } from '../components/use-admin-section.ts'
import { AdminApiError, normalizeAdminApiError } from '../core/api.ts'
import type { EntitlementSummary } from '../core/contracts.ts'
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
const { t } = useAdminI18n()
const { loading, errorCode, reload } = useAdminSectionLoad(
  dashboard,
  () => api.dashboard(),
)

useHead(computed(() => ({
  title: t('document.title'),
})))

const entitlements = computed<readonly EntitlementSummary[]>(() => dashboard.value?.entitlements ?? [])

const saving = ref(false)
const success = ref(false)
const mutationError = ref<AdminApiError['code']>()
const confirmation = ref<{ close(): void, showModal(): void }>()
const selected = ref<EntitlementSummary>()
const action = ref<'assign' | 'revoke'>('assign')
const reason = ref('')
const confirmed = ref(false)

const canSubmit = computed(() =>
  confirmed.value && reason.value.trim().length >= 8 && !saving.value)

function openConfirmation(
  entitlement: EntitlementSummary,
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

    <AdminTable
      v-else
      :items="entitlements"
      :get-key="(entitlement) => entitlement.workspace_id"
      :caption="t('plans.title')"
      :empty-message="t('status.empty')"
    >
      <template #head>
        <th scope="col">
          {{ t('plans.table.workspace') }}
        </th>
        <th scope="col">
          {{ t('plans.table.plan') }}
        </th>
        <th scope="col">
          {{ t('plans.table.type') }}
        </th>
        <th scope="col">
          {{ t('plans.table.actions') }}
        </th>
      </template>
      <template #row="{ item }">
        <td>
          <code>{{ item.workspace_id }}</code>
        </td>
        <td>{{ item.plan_code }}</td>
        <td>{{ item.internal ? t('entitlements.internal') : t('entitlements.public') }}</td>
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
