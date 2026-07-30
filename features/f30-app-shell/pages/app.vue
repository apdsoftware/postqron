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
  buildRegistrationConsents,
  type OAuthProvider,
} from '../components/core/contracts.ts'
import {
  AppNavigationError,
  appRoute,
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
const mode = ref<'register' | 'signin'>('signin')
const accepted = ref(false)
const email = ref('')
const password = ref('')
const confirmation = ref('')
const requestedVerification = ref(false)
const verifyingEmail = ref('')
const submittingPassword = ref(false)
const submittingProvider = ref<OAuthProvider>()
const resendingVerification = ref(false)
const formError = ref<string>()
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
  if (!explicit) {
    return appRoute(locale.value, 'entry')
  }
  return sanitizeAppDestination(explicit)
}

let returnTo = appRoute(locale.value, 'entry')
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
const providers = computed<OAuthProvider[]>(() =>
  bootstrapState.value?.providers ?? [])

async function start(provider: OAuthProvider) {
  if (mode.value === 'register' && !accepted.value) {
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
      contractCountry: mode.value === 'register' ? 'IT' : undefined,
      consents: mode.value === 'register'
        ? buildRegistrationConsents(bootstrapState.value.legal_documents)
        : undefined,
    })
    if (import.meta.client) {
      globalThis.location.assign(authorizationURL)
    }
  } catch (error) {
    formError.value = error instanceof Error ? error.message : 'configuration'
  } finally {
    submittingProvider.value = undefined
  }
}

async function signInWithPassword() {
  submittingPassword.value = true
  formError.value = undefined
  try {
    await api.passwordLogin({
      email: email.value,
      password: password.value,
    })
    password.value = ''
    const session = await api.session()
    sessionState.value = session
    await navigateTo(
      authenticatedDestination(returnTo, session.onboarding_required),
    )
  } catch {
    password.value = ''
    formError.value = 'signin'
  } finally {
    submittingPassword.value = false
  }
}

async function registerWithPassword() {
  if (!bootstrapState.value) {
    formError.value = 'configuration'
    return
  }
  if (!accepted.value) {
    formError.value = 'consent'
    return
  }
  submittingPassword.value = true
  formError.value = undefined
  try {
    await api.passwordRegister({
      email: email.value,
      password: password.value,
      confirmation: confirmation.value,
      consents: buildRegistrationConsents(bootstrapState.value.legal_documents),
    })
    requestedVerification.value = true
    verifyingEmail.value = email.value.trim()
    password.value = ''
    confirmation.value = ''
  } catch {
    formError.value = 'register'
  } finally {
    submittingPassword.value = false
  }
}

async function resendVerification() {
  if (!verifyingEmail.value) {
    return
  }
  resendingVerification.value = true
  formError.value = undefined
  try {
    await api.resendVerification(verifyingEmail.value)
  } catch {
    formError.value = 'resend'
  } finally {
    resendingVerification.value = false
  }
}

async function retry() {
  formError.value = undefined
  await refresh()
}
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
              type="button"
              role="tab"
              :aria-selected="mode === 'register'"
              @click="mode = 'register'"
            >
              {{ t('auth.newAccount') }}
            </button>
            <button
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
                : formError === 'signin'
                  ? t('auth.signInError')
                  : formError === 'register'
                    ? t('auth.registerError')
                    : formError === 'resend'
                      ? t('auth.resendError')
                      : t('auth.configurationError')
            }}
          </div>

          <section
            v-if="requestedVerification"
            class="auth-verification"
          >
            <h2>{{ t('auth.verificationTitle') }}</h2>
            <p>{{ t('auth.verificationDescription', { email: verifyingEmail }) }}</p>
            <div class="auth-verification__actions">
              <button
                class="pq-button"
                data-full-width="true"
                type="button"
                :disabled="resendingVerification"
                @click="resendVerification"
              >
                {{ resendingVerification ? t('auth.resending') : t('auth.resend') }}
              </button>
            </div>
          </section>

          <form
            v-else
            class="auth-password-form"
            @submit.prevent="mode === 'register' ? registerWithPassword() : signInWithPassword()"
          >
            <label for="app-auth-email">{{ t('auth.email') }}</label>
            <input
              id="app-auth-email"
              v-model="email"
              type="email"
              autocomplete="username"
              maxlength="320"
              required
            >
            <label for="app-auth-password">{{ t('auth.password') }}</label>
            <input
              id="app-auth-password"
              v-model="password"
              :autocomplete="mode === 'register' ? 'new-password' : 'current-password'"
              type="password"
              minlength="12"
              maxlength="1024"
              required
            >
            <template v-if="mode === 'register'">
              <label for="app-auth-confirmation">{{ t('auth.confirmation') }}</label>
              <input
                id="app-auth-confirmation"
                v-model="confirmation"
                autocomplete="new-password"
                type="password"
                minlength="12"
                maxlength="1024"
                required
              >
            </template>
            <button
              class="pq-button"
              data-full-width="true"
              type="submit"
              :disabled="submittingPassword"
            >
              <span
                v-if="submittingPassword"
                class="pq-button__spinner"
                aria-hidden="true"
              />
              {{
                submittingPassword
                  ? (mode === 'register' ? t('auth.registering') : t('auth.signingIn'))
                  : (mode === 'register' ? t('auth.registerSubmit') : t('auth.passwordSubmit'))
              }}
            </button>
          </form>

          <label
            v-if="mode === 'register' && !requestedVerification"
            class="auth-consent"
          >
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

          <p
            v-if="providers.length && !requestedVerification"
            class="auth-separator"
          >
            {{ t('auth.orProvider') }}
          </p>

          <div
            v-if="providers.length && !requestedVerification"
            class="auth-providers"
          >
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
          </div>
        </div>
      </section>
    </main>
  </div>
</template>
