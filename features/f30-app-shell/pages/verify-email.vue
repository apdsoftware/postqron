<script setup lang="ts">
import {
  computed,
  definePageMeta,
  onMounted,
  ref,
  useAsyncData,
  useHead,
  useId,
  useRoute,
  watch,
} from '#imports'
import {
  completeEmailVerification,
  emailVerificationDataKey,
  requestEmailVerification,
  withoutEmailVerificationToken,
  withoutEmailVerificationTokenInHistoryState,
} from '../components/core/email-verification.ts'
import { appRoute, localeFromAppPath } from '../components/core/navigation.ts'
import {
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: false })

const route = useRoute()
const api = useAppShellApi()
const { t } = useAppShellI18n()
const locale = computed(() => localeFromAppPath(route.fullPath))
let initialToken = typeof route.query.token === 'string' ? route.query.token : ''
const email = ref(typeof route.query.email === 'string' ? route.query.email : '')
const resendStatus = ref<'idle' | 'success' | 'error'>('idle')
const resending = ref(false)
const verificationDataKey = emailVerificationDataKey(useId())

useHead(computed(() => ({
  title: t('documentTitle.verifyEmail'),
})))

const { data: verification } = await useAsyncData(
  verificationDataKey,
  () => completeEmailVerification(
    initialToken,
    candidate => api.verifyEmail(candidate),
  ),
  {
    default: () => 'no-token' as const,
  },
)
initialToken = ''

function removeTokenFromHistory() {
  const safeLocation = withoutEmailVerificationToken(globalThis.location.href)
  globalThis.history.replaceState(
    withoutEmailVerificationTokenInHistoryState(
      globalThis.history.state,
      safeLocation,
    ),
    '',
    safeLocation,
  )
}

onMounted(() => {
  removeTokenFromHistory()
})

let spaVisit = 0
watch(() => route.query.token, async (value) => {
  const visit = ++spaVisit
  resendStatus.value = 'idle'
  verification.value = 'no-token'
  const result = await completeEmailVerification(
    typeof value === 'string' ? value : '',
    candidate => api.verifyEmail(candidate),
  )
  if (visit === spaVisit) {
    verification.value = result
    removeTokenFromHistory()
  }
})

const messageKey = computed(() => {
  if (verification.value === 'verified') {
    return 'verify.success' as const
  }
  if (resendStatus.value === 'success') {
    return 'verify.resent' as const
  }
  if (verification.value === 'invalid' || resendStatus.value === 'error') {
    return 'verify.error' as const
  }
  return 'verify.description' as const
})

async function resend() {
  if (!email.value.trim()) {
    resendStatus.value = 'error'
    return
  }
  resending.value = true
  resendStatus.value = 'idle'
  const result = await requestEmailVerification(
    email.value,
    candidate => api.resendVerification(candidate),
  )
  email.value = result.email
  resendStatus.value = result.status
  resending.value = false
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

    <main class="onboarding-card">
      <p class="app-eyebrow">
        {{ t('verify.eyebrow') }}
      </p>
      <h1>{{ t('verify.title') }}</h1>
      <p
        class="onboarding-card__lead"
        aria-live="polite"
        :role="messageKey === 'verify.error' ? 'alert' : 'status'"
      >
        {{ t(messageKey) }}
      </p>

      <form
        v-if="verification !== 'verified'"
        class="email-verification__form"
        @submit.prevent="resend"
      >
        <div class="email-verification__fields">
          <label
            class="app-field"
            for="verification-email"
          >
            <span>{{ t('verify.email') }}</span>
            <input
              id="verification-email"
              v-model="email"
              type="email"
              autocomplete="email"
              maxlength="320"
              required
            >
          </label>
        </div>
        <button
          class="pq-button"
          data-full-width="true"
          type="submit"
          :disabled="resending"
        >
          {{ resending ? t('verify.resending') : t('verify.resend') }}
        </button>
        <a
          class="pq-button pq-button--secondary"
          :href="appRoute(locale, 'entry')"
        >
          {{ t('verify.return') }}
        </a>
      </form>
      <div
        v-else
        class="email-verification__success-actions"
      >
        <a
          class="pq-button"
          :href="appRoute(locale, 'entry')"
        >
          {{ t('verify.return') }}
        </a>
      </div>
    </main>
  </div>
</template>
