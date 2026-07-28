<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useHead,
  useRoute,
} from '#imports'
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
const token = computed(() => typeof route.query.token === 'string' ? route.query.token : '')
const email = ref(typeof route.query.email === 'string' ? route.query.email : '')
const status = ref<'idle' | 'success' | 'error'>('idle')
const resending = ref(false)

useHead(computed(() => ({
  title: t('documentTitle.verifyEmail'),
})))

if (token.value) {
  try {
    await api.verifyEmail(token.value)
    status.value = 'success'
  } catch {
    status.value = 'error'
  }
}

async function resend() {
  if (!email.value.trim()) {
    status.value = 'error'
    return
  }
  resending.value = true
  try {
    await api.resendVerification(email.value)
    status.value = 'success'
  } catch {
    status.value = 'error'
  } finally {
    resending.value = false
  }
}
</script>

<template>
  <div class="onboarding-page">
    <header class="auth-page__header">
      <a href="/" aria-label="Postqron">
        <img src="/brand/logo-primary.svg" alt="Postqron">
      </a>
      <PostqronLanguageSwitcher />
    </header>

    <main class="onboarding-card">
      <p class="app-eyebrow">{{ t('verify.eyebrow') }}</p>
      <h1>{{ t('verify.title') }}</h1>
      <p class="onboarding-card__lead">
        {{
          status === 'success'
            ? t('verify.success')
            : status === 'error'
              ? t('verify.error')
              : t('verify.description')
        }}
      </p>

      <form class="app-form-grid" @submit.prevent="resend">
        <label class="app-field">
          <span>{{ t('verify.email') }}</span>
          <input v-model="email" type="email" maxlength="320" required>
        </label>
        <button class="pq-button" type="submit" :disabled="resending">
          {{ resending ? t('verify.resending') : t('verify.resend') }}
        </button>
        <a class="pq-button pq-button--secondary" :href="appRoute(locale, 'entry')">
          {{ t('verify.return') }}
        </a>
      </form>
    </main>
  </div>
</template>
