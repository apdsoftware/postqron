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
  useAppBootstrapState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'
import { formatDateTime } from '../components/core/preferences.ts'
import type { OAuthProvider } from '../components/core/contracts.ts'

definePageMeta({ layout: 'app-shell' })

const api = useAppShellApi()
const route = useRoute()
const bootstrap = useAppBootstrapState()
const accountArea = useAppAccountAreaState()
const { t, locale: uiLocale } = useAppShellI18n()
const linking = ref<OAuthProvider>()
const disconnecting = ref<string>()
const feedback = ref<'error' | 'linked' | 'removed'>()
const locale = computed(() => localeFromAppPath(route.fullPath))
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()

useHead(computed(() => ({
  title: t('documentTitle.providers'),
})))

const { pending, refresh } = useAsyncData('postqron-account-providers', async () => {
  try {
    if (!bootstrap.value) {
      bootstrap.value = await api.bootstrap()
    }
    accountArea.value = await api.accountArea()
    pageState.value = undefined
    return {
      bootstrap: bootstrap.value,
      account: accountArea.value,
    }
  } catch (error) {
    accountArea.value = undefined
    pageState.value = appStateKindFromError(error)
    return undefined
  }
}, { server: false })

const linkedProviderNames = computed(() =>
  new Set((accountArea.value?.providers ?? []).map(provider => provider.name)))
const availableIdentityProviders = computed(() =>
  (bootstrap.value?.providers ?? []).filter(provider => !linkedProviderNames.value.has(provider)))
const connectedMethods = computed(() =>
  (accountArea.value?.providers ?? []).filter(provider => provider.kind === 'identity'))

async function linkProvider(provider: OAuthProvider) {
  linking.value = provider
  feedback.value = undefined
  try {
    const authorizationURL = await api.linkProvider({
      provider,
      returnTo: appRoute(locale.value, 'providers'),
    })
    if (import.meta.client) {
      globalThis.location.assign(authorizationURL)
    }
  } catch {
    feedback.value = 'error'
  } finally {
    linking.value = undefined
  }
}

async function disconnectProvider(providerId: string) {
  disconnecting.value = providerId
  feedback.value = undefined
  try {
    await api.disconnectProvider(providerId)
    accountArea.value = await api.accountArea()
    feedback.value = 'removed'
  } catch {
    feedback.value = 'error'
  } finally {
    disconnecting.value = undefined
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
      {{ t('providers.eyebrow') }}
    </p>
    <h1>{{ t('providers.title') }}</h1>
    <p class="app-page__lead">
      {{ t('providers.description') }}
    </p>
    <p class="app-inline-note">
      {{ t('providers.loginScopeNote') }}
    </p>

    <div class="app-page__stack">
      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('providers.connected') }}</span>
          <h2>{{ t('providers.connectedTitle') }}</h2>
        </div>
        <AppState
          v-if="connectedMethods.length === 0"
          kind="empty"
        />
        <ul
          v-else
          class="app-provider-list"
        >
          <li
            v-for="provider in connectedMethods"
            :key="provider.id"
          >
            <div class="app-provider-list__meta">
              <strong>{{ provider.name }}</strong>
              <span>{{ formatDateTime(provider.connected_at, uiLocale.value) }}</span>
            </div>
            <button
              class="pq-button pq-button--secondary"
              type="button"
              :disabled="disconnecting === provider.id || provider.only_login_method"
              @click="disconnectProvider(provider.id)"
            >
              {{
                provider.only_login_method
                  ? t('providers.required')
                  : disconnecting === provider.id
                    ? t('providers.disconnecting')
                    : t('providers.disconnect')
              }}
            </button>
          </li>
        </ul>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('providers.linkNew') }}</span>
          <h2>{{ t('providers.linkNewTitle') }}</h2>
        </div>
        <div class="app-action-stack">
          <button
            v-for="provider in availableIdentityProviders"
            :key="provider"
            class="auth-provider"
            type="button"
            :disabled="linking === provider"
            @click="linkProvider(provider)"
          >
            <span
              class="auth-provider__mark"
              aria-hidden="true"
            >{{ provider.slice(0, 1).toUpperCase() }}</span>
            {{ t(`auth.provider.${provider}`) }}
            <span
              v-if="linking === provider"
              class="pq-button__spinner"
              aria-hidden="true"
            />
          </button>
          <p
            v-if="availableIdentityProviders.length === 0"
            class="app-inline-note"
          >
            {{ t('providers.noAvailable') }}
          </p>
        </div>
      </article>
    </div>

    <p
      v-if="feedback"
      class="app-inline-alert"
      :data-success="feedback !== 'error'"
      role="status"
    >
      {{
        feedback === 'removed'
          ? t('providers.removed')
          : feedback === 'linked'
            ? t('providers.linked')
            : t('providers.error')
      }}
    </p>
  </section>
</template>
