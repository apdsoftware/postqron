<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useHead,
} from '#imports'
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

useHead(computed(() => ({
  title: `${t('plans.page.title')} — Postqron`,
})))

const loading = ref(true)
const saving = ref(false)
const errorCode = ref<AdminApiError['code']>()
const success = ref(false)
const confirmation = ref<{ close(): void, showModal(): void }>()
const selected = ref<EntitlementSummary>()
const action = ref<'assign' | 'revoke'>('assign')
const reason = ref('')
const confirmed = ref(false)

const canSubmit = computed(() =>
  confirmed.value && reason.value.trim().length >= 8 && !saving.value)

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

function openConfirmation(
  entitlement: EntitlementSummary,
  nextAction: 'assign' | 'revoke',
) {
  selected.value = entitlement
  action.value = nextAction
  reason.value = ''
  confirmed.value = false
  success.value = false
  errorCode.value = undefined
  confirmation.value?.showModal()
}

function closeConfirmation() {
  if (!saving.value) {
    confirmation.value?.close()
  }
}

async function applyInternalPlan() {
  if (!selected.value || !session.value || !canSubmit.value) {
    errorCode.value = 'ADMIN_INVALID_REQUEST'
    return
  }
  saving.value = true
  errorCode.value = undefined
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
    await loadDashboard()
  } catch (error) {
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    saving.value = false
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
      :eyebrow="t('plans.page.eyebrow')"
      :title="t('plans.page.title')"
      :description="t('plans.page.description')"
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
      aria-labelledby="admin-plans-title"
    >
      <h2 id="admin-plans-title">
        {{ t('entitlements.title') }}
      </h2>
      <AdminDataTable
        :caption="t('entitlements.title')"
        :columns="[
          { key: 'workspace', label: t('plans.table.workspace') },
          { key: 'plan', label: t('plans.table.plan') },
          { key: 'status', label: t('plans.table.status') },
          { key: 'actions', label: t('plans.table.actions') },
        ]"
        :rows="dashboard.entitlements.map(entitlement => ({
          workspace: entitlement.workspace_id,
          plan: entitlement.plan_code,
          status: entitlement.internal ? t('entitlements.internal') : t('entitlements.public'),
        }))"
        :empty-message="t('status.empty')"
      >
        <template #cell-actions="{ row }">
          <button
            class="pq-button"
            :class="row.status === t('entitlements.internal') ? 'pq-button--secondary' : 'pq-button--primary'"
            type="button"
            @click="openConfirmation(
              dashboard.entitlements.find(entitlement => entitlement.workspace_id === row.workspace)!,
              row.status === t('entitlements.internal') ? 'revoke' : 'assign',
            )"
          >
            {{ row.status === t('entitlements.internal') ? t('entitlements.revoke') : t('entitlements.assign') }}
          </button>
        </template>
      </AdminDataTable>
    </section>

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
          v-if="errorCode"
          class="admin-inline-error"
          role="alert"
        >
          {{ t(`error.${errorCode}` as never) }}
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
