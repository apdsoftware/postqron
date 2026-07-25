<script setup lang="ts">
import {
  computed,
  definePageMeta,
  navigateTo,
  ref,
  useAsyncData,
  useHead,
  useRequestHeaders,
  useRoute,
} from '#imports'
import {
  buildConsentReceipts,
  type OAuthProvider,
} from '../components/core/contracts.ts'
import {
  AppNavigationError,
  appRoot,
  authenticatedDestination,
  localeFromAppPath,
  sanitizeAppDestination,
} from '../components/core/navigation.ts'
import {
  useAppBootstrapState,
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: false })

const route = useRoute()
const api = useAppShellApi()
const { t } = useAppShellI18n()
const bootstrapState = useAppBootstrapState()
const sessionState = useAppSessionState()
const accepted = ref(false)
const mode = ref<'register' | 'signin'>('register')
const submittingProvider = ref<OAuthProvider>()
const formError = ref<'configuration' | 'consent' | 'offline'>()
const invalidIntent = ref(false)
const locale = computed(() => localeFromAppPath(route.fullPath))

useHead(computed(() => ({
  title: t('documentTitle.app'),
})))

function queryString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function resolveReturnTo(): string {
  const explicit = queryString(route.query.return_to)
  if (explicit) {
    return sanitizeAppDestination(explicit)
  }
  const parameters: string[] = []
  for (const key of ['plan', 'interval', 'quantity'] as const) {
    const value = queryString(route.query[key])
    if (value !== undefined) {
      parameters.push(`${key}=${encodeURIComponent(value)}`)
    }
  }
  const target = appRoot(locale.value)
  return sanitizeAppDestination(
    parameters.length ? `${target}?${parameters.join('&')}` : target,
  )
}

let returnTo = appRoot(locale.value)
try {
  returnTo = resolveReturnTo()
} catch (error) {
  invalidIntent.value = error instanceof AppNavigationError
}

const requestedState = computed(() => {
  const state = queryString(route.query.app_state)
  return state === 'offline' || state === 'access-denied' ? state : undefined
})

const {
  pending,
  refresh,
  status,
} = await useAsyncData('postqron-app-bootstrap', async () => {
  const headers = import.meta.server ? useRequestHeaders(['cookie']) : undefined
  const bootstrap = await api.bootstrap(headers)
  bootstrapState.value = bootstrap
  if (bootstrap.session) {
    sessionState.value = bootstrap.session
    await navigateTo(
      authenticatedDestination(returnTo, bootstrap.session.onboarding_required),
      { redirectCode: 302 },
    )
  }
  return bootstrap
})

const terms = computed(() =>
  bootstrapState.value?.legal_documents.find(document => document.key === 'terms'))
const privacy = computed(() =>
  bootstrapState.value?.legal_documents.find(document => document.key === 'privacy'))

async function start(provider: OAuthProvider) {
  if (!accepted.value) {
    formError.value = 'consent'
    return
  }
  if (!bootstrapState.value) {
    formError.value = 'configuration'
    return
  }
  submittingProvider.value = provider
  formError.value = undefined
  try {
    const authorizationURL = await api.authorize({
      provider,
      returnTo,
      contractCountry: 'IT',
      consents: buildConsentReceipts(
        bootstrapState.value.legal_documents,
        locale.value,
      ),
    })
    if (import.meta.client) {
      globalThis.location.assign(authorizationURL)
    }
  } catch {
    formError.value = globalThis.navigator && !globalThis.navigator.onLine
      ? 'offline'
      : 'configuration'
  } finally {
    submittingProvider.value = undefined
  }
}

async function retry() {
  formError.value = undefined
  await refresh()
}

const providers = computed<OAuthProvider[]>(() =>
  bootstrapState.value?.providers ?? [])
</script>

<template>
  <div class="auth-page">
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

    <main class="auth-page__main">
      <section class="auth-intro">
        <p class="app-eyebrow">
          {{ t('auth.eyebrow') }}
        </p>
        <h1>{{ t('auth.title') }}</h1>
        <p>{{ t('auth.description') }}</p>
        <div
          class="auth-intro__art"
          aria-hidden="true"
        >
          <span />
          <span />
          <span />
        </div>
      </section>

      <section
        class="auth-card"
        aria-live="polite"
      >
        <AppState
          v-if="pending"
          kind="loading"
        />
        <AppState
          v-else-if="requestedState === 'offline' || status === 'error'"
          kind="offline"
          action
          @retry="retry"
        />
        <AppState
          v-else-if="requestedState === 'access-denied'"
          kind="access-denied"
        />
        <div v-else>
          <div
            class="auth-tabs"
            role="tablist"
          >
            <button
              id="register-tab"
              type="button"
              role="tab"
              :aria-selected="mode === 'register'"
              @click="mode = 'register'"
            >
              {{ t('auth.newAccount') }}
            </button>
            <button
              id="signin-tab"
              type="button"
              role="tab"
              :aria-selected="mode === 'signin'"
              @click="mode = 'signin'"
            >
              {{ t('auth.existingAccount') }}
            </button>
          </div>

          <div
            v-if="invalidIntent"
            class="app-inline-alert"
            role="alert"
          >
            {{ t('auth.invalidParameters') }}
          </div>
          <div
            v-if="formError"
            class="app-inline-alert"
            role="alert"
          >
            {{
              formError === 'consent'
                ? t('auth.requiredConsent')
                : formError === 'offline'
                  ? t('auth.offline')
                  : t('auth.configurationError')
            }}
          </div>

          <div class="auth-providers">
            <button
              v-for="provider in providers"
              :key="provider"
              class="auth-provider"
              type="button"
              :disabled="Boolean(submittingProvider) || invalidIntent"
              @click="start(provider)"
            >
              <span
                class="auth-provider__mark"
                aria-hidden="true"
              >{{ provider.slice(0, 1).toUpperCase() }}</span>
              {{ t(`auth.provider.${provider}`) }}
              <span
                v-if="submittingProvider === provider"
                class="pq-button__spinner"
                aria-hidden="true"
              />
            </button>
            <p
              v-if="providers.length === 0"
              class="auth-card__muted"
            >
              {{ t('auth.providerUnavailable') }}
            </p>
          </div>

          <label class="auth-consent">
            <input
              v-model="accepted"
              type="checkbox"
            >
            <span>
              {{ t('auth.consentBefore') }}
              <a
                :href="terms?.href"
                target="_blank"
              >{{ t('auth.terms') }}</a>
              {{ t('auth.consentAnd') }}
              <a
                :href="privacy?.href"
                target="_blank"
              >{{ t('auth.privacy') }}</a>{{ t('auth.consentAfter') }}
            </span>
          </label>
        </div>
      </section>
    </main>
  </div>
</template>
