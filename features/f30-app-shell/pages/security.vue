<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useAsyncData,
  useHead,
  useRoute,
} from '#imports'
import { appRoute, localeFromAppPath } from '../components/core/navigation.ts'
import {
  appStateKindFromError,
  useAppAccountAreaState,
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'
import { formatDateTime } from '../components/core/preferences.ts'

definePageMeta({ layout: 'app-shell' })

const api = useAppShellApi()
const route = useRoute()
const session = useAppSessionState()
const accountArea = useAppAccountAreaState()
const { t, locale: uiLocale } = useAppShellI18n()
const currentPassword = ref('')
const newPassword = ref('')
const confirmation = ref('')
const changing = ref(false)
const revoking = ref(false)
const feedback = ref<'changed' | 'error' | 'revoked'>()
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()
const locale = computed(() => localeFromAppPath(route.fullPath))

useHead(computed(() => ({
  title: t('documentTitle.security'),
})))

const { pending, refresh } = useAsyncData('postqron-account-security', async () => {
  try {
    accountArea.value = await api.accountArea()
    pageState.value = undefined
    return accountArea.value
  } catch (error) {
    accountArea.value = undefined
    pageState.value = appStateKindFromError(error)
    return undefined
  }
}, { server: false })

const identityProviders = computed(() =>
  accountArea.value?.providers.filter(provider => provider.kind === 'identity') ?? [])
const emailVerified = computed(() => session.value?.account.email_verified ?? false)

async function changePassword() {
  changing.value = true
  feedback.value = undefined
  try {
    await api.changePassword({
      currentPassword: currentPassword.value,
      newPassword: newPassword.value,
      confirmation: confirmation.value,
    })
    currentPassword.value = ''
    newPassword.value = ''
    confirmation.value = ''
    feedback.value = 'changed'
    session.value = await api.session()
  } catch {
    feedback.value = 'error'
  } finally {
    changing.value = false
  }
}

async function revokeSessions() {
  revoking.value = true
  feedback.value = undefined
  try {
    await api.revokeSessions()
    feedback.value = 'revoked'
  } catch {
    feedback.value = 'error'
  } finally {
    revoking.value = false
  }
}

async function retry() {
  await refresh()
}
</script>

<template>
  <AppState
    v-if="pending && !accountArea"
    kind="loading"
  />
  <AppState
    v-else-if="pageState"
    :kind="pageState"
    action
    @retry="retry"
  />
  <section
    v-else
    class="app-page"
  >
    <p class="app-eyebrow">
      {{ t('security.eyebrow') }}
    </p>
    <h1>{{ t('security.title') }}</h1>
    <p class="app-page__lead">
      {{ t('security.description') }}
    </p>

    <div class="app-page__stack">
      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('security.identitySection') }}</span>
          <h2>{{ t('security.identityTitle') }}</h2>
        </div>
        <dl class="app-detail-list">
          <div class="app-inline-meta">
            <dt>{{ t('security.emailStatus') }}</dt>
            <dd>{{ session?.account.email || t('security.emailMissing') }}</dd>
          </div>
          <div class="app-inline-meta">
            <dt>{{ t('security.emailVerification') }}</dt>
            <dd>
              <span
                class="app-badge"
                :class="emailVerified ? 'app-badge--success' : 'app-badge--warning'"
              >
                {{ emailVerified ? t('security.verified') : t('security.unverified') }}
              </span>
            </dd>
          </div>
        </dl>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('security.methods') }}</span>
          <h2>{{ t('security.methodsTitle') }}</h2>
        </div>
        <p class="app-inline-note">
          {{ t('security.methodsNote') }}
        </p>
        <ul class="app-provider-list">
          <li
            v-for="provider in identityProviders"
            :key="provider.id"
          >
            <div class="app-provider-list__meta">
              <strong>{{ provider.name }}</strong>
              <span>{{ formatDateTime(provider.connected_at, uiLocale.value) }}</span>
            </div>
            <span
              v-if="provider.only_login_method"
              class="app-badge app-badge--info"
            >{{ t('security.onlyMethod') }}</span>
          </li>
        </ul>
        <NuxtLink
          class="pq-button pq-button--secondary"
          :to="appRoute(locale, 'providers')"
        >
          {{ t('security.manageMethodsLink') }}
        </NuxtLink>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('security.changePassword') }}</span>
          <h2>{{ t('security.changePasswordTitle') }}</h2>
        </div>
        <form
          class="app-form-grid"
          @submit.prevent="changePassword"
        >
          <label class="app-field">
            <span>{{ t('security.currentPassword') }}</span>
            <input
              v-model="currentPassword"
              type="password"
              minlength="12"
              required
              autocomplete="current-password"
            >
          </label>
          <label class="app-field">
            <span>{{ t('security.newPassword') }}</span>
            <input
              v-model="newPassword"
              type="password"
              minlength="12"
              required
              autocomplete="new-password"
            >
          </label>
          <label class="app-field">
            <span>{{ t('security.confirmPassword') }}</span>
            <input
              v-model="confirmation"
              type="password"
              minlength="12"
              required
              autocomplete="new-password"
            >
          </label>
          <button
            class="pq-button"
            type="submit"
            :disabled="changing"
          >
            {{ changing ? t('security.changing') : t('security.changeSubmit') }}
          </button>
        </form>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('security.sessions') }}</span>
          <h2>{{ t('security.sessionsTitle') }}</h2>
        </div>
        <p>{{ t('security.sessionsDescription') }}</p>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          :disabled="revoking"
          @click="revokeSessions"
        >
          {{ revoking ? t('security.revoking') : t('security.revokeSubmit') }}
        </button>
      </article>
    </div>

    <p
      v-if="feedback"
      class="app-inline-alert"
      :data-success="feedback !== 'error'"
      role="status"
    >
      {{
        feedback === 'changed'
          ? t('security.changed')
          : feedback === 'revoked'
            ? t('security.revoked')
            : t('security.error')
      }}
    </p>
  </section>
</template>
