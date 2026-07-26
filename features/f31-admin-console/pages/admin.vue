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
  useAdminSearchState,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const api = useAdminApi()
const session = useAdminSessionState()
const dashboard = useAdminDashboardState()
const searchResults = useAdminSearchState()
const { date, t } = useAdminI18n()
const loading = ref(true)
const authenticating = ref(false)
const searching = ref(false)
const saving = ref(false)
const email = ref('')
const password = ref('')
const query = ref('')
const errorCode = ref<AdminApiError['code']>()
const success = ref(false)
const confirmation = ref<{ close(): void, showModal(): void }>()
const selected = ref<EntitlementSummary>()
const action = ref<'assign' | 'revoke'>('assign')
const reason = ref('')
const confirmed = ref(false)

useHead(computed(() => ({
  title: t('document.title'),
})))

const canSubmit = computed(() =>
  confirmed.value && reason.value.trim().length >= 8 && !saving.value)

async function login() {
  authenticating.value = true
  errorCode.value = undefined
  try {
    await api.passwordLogin({
      email: email.value,
      password: password.value,
    })
    password.value = ''
    session.value = await api.session()
    await loadDashboard()
  } catch (error) {
    password.value = ''
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    authenticating.value = false
  }
}

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

async function search() {
  searching.value = true
  errorCode.value = undefined
  try {
    searchResults.value = await api.search(query.value)
  } catch (error) {
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    searching.value = false
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
    <header class="admin-page__heading">
      <p class="admin-eyebrow">
        {{ t('page.eyebrow') }}
      </p>
      <h1>{{ t('page.title') }}</h1>
      <p>{{ t('page.description') }}</p>
    </header>

    <section
      v-if="!session"
      class="admin-panel admin-login"
      aria-labelledby="admin-login-title"
    >
      <h2 id="admin-login-title">
        {{ t('login.title') }}
      </h2>
      <p>{{ t('login.description') }}</p>
      <form @submit.prevent="login">
        <label for="admin-email">{{ t('login.email') }}</label>
        <input
          id="admin-email"
          v-model="email"
          type="email"
          autocomplete="username"
          maxlength="320"
          required
        >
        <label for="admin-password">{{ t('login.password') }}</label>
        <input
          id="admin-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          minlength="12"
          maxlength="1024"
          required
        >
        <p
          v-if="errorCode"
          class="admin-inline-error"
          role="alert"
        >
          {{ t(`error.${errorCode}` as never) }}
        </p>
        <button
          class="pq-button pq-button--primary"
          type="submit"
          :disabled="authenticating"
        >
          {{ authenticating ? t('login.signingIn') : t('login.submit') }}
        </button>
      </form>
    </section>

    <p
      v-else-if="loading"
      class="admin-state"
      role="status"
      aria-live="polite"
    >
      {{ t('status.loading') }}
    </p>
    <div
      v-else-if="errorCode && !dashboard"
      class="admin-state admin-state--error"
      role="alert"
    >
      <p>{{ t(`error.${errorCode}` as never) }}</p>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        @click="loadDashboard"
      >
        {{ t('status.retry') }}
      </button>
    </div>

    <template v-else-if="dashboard">
      <section
        class="admin-panel"
        aria-labelledby="admin-health-title"
      >
        <h2 id="admin-health-title">
          {{ t('health.title') }}
        </h2>
        <ul class="admin-health">
          <li
            v-for="service in dashboard.services"
            :key="service.code"
          >
            <span
              class="admin-status-dot"
              :data-status="service.status"
              aria-hidden="true"
            />
            <strong>{{ service.code }}</strong>
            <span>{{ service.status === 'healthy' ? t('health.healthy') : t('health.degraded') }}</span>
            <time :datetime="service.checked_at">{{ date(service.checked_at) }}</time>
          </li>
        </ul>
      </section>

      <section
        class="admin-panel"
        aria-labelledby="admin-search-title"
      >
        <h2 id="admin-search-title">
          {{ t('search.title') }}
        </h2>
        <form
          class="admin-search"
          @submit.prevent="search"
        >
          <label for="admin-search">{{ t('search.label') }}</label>
          <div>
            <input
              id="admin-search"
              v-model="query"
              type="search"
              minlength="2"
              maxlength="120"
              :placeholder="t('search.placeholder')"
              required
            >
            <button
              class="pq-button pq-button--primary"
              type="submit"
              :disabled="searching"
            >
              {{ t('search.submit') }}
            </button>
          </div>
        </form>
        <div
          v-if="searchResults"
          class="admin-search-results"
        >
          <section>
            <h3>{{ t('search.users') }}</h3>
            <p v-if="searchResults.users.length === 0">
              {{ t('status.empty') }}
            </p>
            <ul>
              <li
                v-for="user in searchResults.users"
                :key="user.id"
              >
                <strong>{{ user.display_name }}</strong>
                <span>{{ user.email }}</span>
                <small>{{ user.email_verified ? t('search.verified') : t('search.unverified') }}</small>
              </li>
            </ul>
          </section>
          <section>
            <h3>{{ t('search.workspaces') }}</h3>
            <p v-if="searchResults.workspaces.length === 0">
              {{ t('status.empty') }}
            </p>
            <ul>
              <li
                v-for="workspace in searchResults.workspaces"
                :key="workspace.id"
              >
                <strong>{{ workspace.name }}</strong>
                <span>{{ workspace.owner_email }}</span>
                <code>{{ workspace.id }}</code>
              </li>
            </ul>
          </section>
        </div>
      </section>

      <section
        class="admin-panel"
        aria-labelledby="admin-entitlements-title"
      >
        <h2 id="admin-entitlements-title">
          {{ t('entitlements.title') }}
        </h2>
        <ul class="admin-entitlements">
          <li
            v-for="entitlement in dashboard.entitlements"
            :key="entitlement.workspace_id"
          >
            <div>
              <code>{{ entitlement.workspace_id }}</code>
              <strong>{{ entitlement.plan_code }}</strong>
              <span>{{ entitlement.internal ? t('entitlements.internal') : t('entitlements.public') }}</span>
            </div>
            <button
              class="pq-button"
              :class="entitlement.internal ? 'pq-button--secondary' : 'pq-button--primary'"
              type="button"
              @click="openConfirmation(entitlement, entitlement.internal ? 'revoke' : 'assign')"
            >
              {{ entitlement.internal ? t('entitlements.revoke') : t('entitlements.assign') }}
            </button>
          </li>
        </ul>
      </section>

      <section
        class="admin-panel"
        aria-labelledby="admin-audit-title"
      >
        <h2 id="admin-audit-title">
          {{ t('audit.title') }}
        </h2>
        <ol class="admin-audit">
          <li
            v-for="event in dashboard.recent_audit"
            :key="event.id"
          >
            <div>
              <code>{{ event.code }}</code>
              <time :datetime="event.occurred_at">{{ date(event.occurred_at) }}</time>
            </div>
            <dl>
              <dt>{{ t('audit.outcome') }}</dt>
              <dd>{{ event.outcome }}</dd>
              <dt>{{ t('audit.reason') }}</dt>
              <dd>{{ event.reason }}</dd>
            </dl>
            <small>{{ event.correlation_id }}</small>
          </li>
        </ol>
      </section>
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
