<script setup lang="ts">
import {
  computed,
  definePageMeta,
  navigateTo,
  ref,
  useAsyncData,
  useHead,
  useRoute,
} from '#imports'
import { buildConsentReceipts } from '../components/core/contracts.ts'
import {
  appRoot,
  localeFromAppPath,
  safeAppDestination,
} from '../components/core/navigation.ts'
import {
  appStateKindFromError,
  useAppBootstrapState,
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: false })

const route = useRoute()
const api = useAppShellApi()
const { t } = useAppShellI18n()
const session = useAppSessionState()
const bootstrap = useAppBootstrapState()
const locale = computed(() => localeFromAppPath(route.fullPath))
const mode = ref<'create' | 'select'>(
  session.value?.workspaces.length ? 'select' : 'create',
)
const workspaceName = ref('')
const selectedWorkspace = ref(session.value?.workspaces[0]?.id ?? '')
const accepted = ref(false)
const saving = ref(false)
const error = ref(false)
const pageState = ref<'access-denied' | 'offline'>()

useHead(computed(() => ({
  title: t('documentTitle.onboarding'),
})))

const { pending, refresh } = useAsyncData('postqron-onboarding-bootstrap', async () => {
  try {
    const value = await api.bootstrap()
    bootstrap.value = value
    pageState.value = undefined
    return value
  } catch (loadError) {
    bootstrap.value = undefined
    pageState.value = appStateKindFromError(loadError)
    return undefined
  }
}, { server: false })

const returnTo = computed(() => safeAppDestination(
  typeof route.query.return_to === 'string'
    ? route.query.return_to
    : `${appRoot(locale.value)}/home`,
  locale.value,
))

async function submit() {
  if (!accepted.value || !bootstrap.value) {
    error.value = true
    return
  }
  saving.value = true
  error.value = false
  try {
    const workspace = mode.value === 'create'
      ? { mode: 'create' as const, name: workspaceName.value.trim() }
      : { mode: 'select' as const, id: selectedWorkspace.value }
    session.value = await api.completeOnboarding({
      consents: buildConsentReceipts(
        bootstrap.value.legal_documents,
        locale.value,
      ),
      workspace,
    })
    await navigateTo(returnTo.value)
  } catch {
    error.value = true
  } finally {
    saving.value = false
  }
}

async function retry() {
  await refresh()
}
</script>

<template>
  <div class="onboarding-page">
    <header class="auth-page__header">
      <a
        href="/"
        aria-label="Postqron"
      >
        <img
          src="/brand/logo-primary.svg"
          alt="Postqron"
        >
      </a>
      <PostqronLanguageSwitcher />
    </header>

    <AppState
      v-if="pending && !bootstrap"
      kind="loading"
    />
    <AppState
      v-else-if="pageState"
      :kind="pageState"
      action
      @retry="retry"
    />
    <main
      v-else
      class="onboarding-card"
    >
      <p class="app-eyebrow">
        {{ t('onboarding.eyebrow') }}
      </p>
      <h1>{{ t('onboarding.title') }}</h1>
      <p class="onboarding-card__lead">
        {{ t('onboarding.description') }}
      </p>

      <form @submit.prevent="submit">
        <fieldset class="onboarding-choice">
          <legend class="pq-visually-hidden">
            {{ t('onboarding.workspaceChoice') }}
          </legend>
          <label>
            <input
              v-model="mode"
              type="radio"
              value="create"
            >
            <span>{{ t('onboarding.create') }}</span>
          </label>
          <label v-if="session?.workspaces.length">
            <input
              v-model="mode"
              type="radio"
              value="select"
            >
            <span>{{ t('onboarding.select') }}</span>
          </label>
        </fieldset>

        <label
          v-if="mode === 'create'"
          class="app-field"
        >
          <span>{{ t('onboarding.workspaceName') }}</span>
          <input
            v-model="workspaceName"
            type="text"
            maxlength="80"
            required
            autocomplete="organization"
            :placeholder="t('onboarding.workspacePlaceholder')"
          >
        </label>
        <label
          v-else
          class="app-field"
        >
          <span>{{ t('onboarding.workspaceChoice') }}</span>
          <select
            v-model="selectedWorkspace"
            required
          >
            <option
              v-for="workspace in session?.workspaces ?? []"
              :key="workspace.id"
              :value="workspace.id"
            >
              {{ workspace.name }}
            </option>
          </select>
        </label>

        <label class="auth-consent">
          <input
            v-model="accepted"
            type="checkbox"
            required
          >
          <span>
            {{ t('auth.consentBefore') }}
            <a
              :href="bootstrap?.legal_documents.find(document => document.key === 'terms')?.href"
              target="_blank"
            >{{ t('auth.terms') }}</a>
            {{ t('auth.consentAnd') }}
            <a
              :href="bootstrap?.legal_documents.find(document => document.key === 'privacy')?.href"
              target="_blank"
            >{{ t('auth.privacy') }}</a>{{ t('auth.consentAfter') }}
          </span>
        </label>

        <p
          v-if="error"
          class="app-inline-alert"
          role="alert"
        >
          {{ t('onboarding.error') }}
        </p>
        <button
          class="pq-button"
          data-full-width="true"
          type="submit"
          :disabled="saving"
        >
          <span
            v-if="saving"
            class="pq-button__spinner"
            aria-hidden="true"
          />
          {{ saving ? t('onboarding.saving') : t('onboarding.submit') }}
        </button>
      </form>
    </main>
  </div>
</template>
