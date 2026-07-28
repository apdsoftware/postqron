<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useAsyncData,
  useHead,
} from '#imports'
import {
  appStateKindFromError,
  useAppAccountAreaState,
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: 'app-shell' })

const api = useAppShellApi()
const accountArea = useAppAccountAreaState()
const session = useAppSessionState()
const { t } = useAppShellI18n()
const saving = ref(false)
const feedback = ref<'error' | 'saved'>()
const displayName = ref('')
const locale = ref('it-IT')
const timezone = ref('Europe/Rome')
const pageState = ref<'access-denied' | 'offline'>()

useHead(computed(() => ({
  title: t('documentTitle.profile'),
})))

const { pending, refresh } = useAsyncData('postqron-account-profile', async () => {
  try {
    accountArea.value = await api.accountArea()
    displayName.value = accountArea.value.profile.display_name
    locale.value = accountArea.value.profile.locale
    timezone.value = accountArea.value.profile.timezone
    pageState.value = undefined
    return accountArea.value
  } catch (error) {
    accountArea.value = undefined
    pageState.value = appStateKindFromError(error)
    return undefined
  }
})

async function saveProfile() {
  saving.value = true
  feedback.value = undefined
  try {
    const profile = await api.updateProfile({
      displayName: displayName.value,
      locale: locale.value,
      timezone: timezone.value,
    })
    if (accountArea.value) {
      accountArea.value = { ...accountArea.value, profile }
    }
    feedback.value = 'saved'
  } catch {
    feedback.value = 'error'
  } finally {
    saving.value = false
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
    <p class="app-eyebrow">{{ t('profile.eyebrow') }}</p>
    <h1>{{ t('profile.title') }}</h1>
    <p class="app-page__lead">{{ t('profile.description') }}</p>

    <form class="app-form-grid" @submit.prevent="saveProfile">
      <label class="app-field">
        <span>{{ t('profile.displayName') }}</span>
        <input v-model="displayName" type="text" maxlength="100" required>
      </label>
      <label class="app-field">
        <span>{{ t('profile.email') }}</span>
        <input :value="session?.account.email || ''" type="email" disabled>
      </label>
      <label class="app-field">
        <span>{{ t('profile.locale') }}</span>
        <input v-model="locale" type="text" required>
      </label>
      <label class="app-field">
        <span>{{ t('profile.timezone') }}</span>
        <input v-model="timezone" type="text" required>
      </label>
      <div class="app-inline-meta">
        <strong>{{ t('profile.updatedAt') }}</strong>
        <span>{{ accountArea?.profile.updated_at }}</span>
      </div>
      <div class="app-inline-meta">
        <strong>{{ t('profile.emailStatus') }}</strong>
        <span>{{ session?.account.email_verified ? t('profile.verified') : t('profile.unverified') }}</span>
      </div>
      <p
        v-if="feedback"
        class="app-inline-alert"
        :data-success="feedback === 'saved'"
        role="status"
      >
        {{ feedback === 'saved' ? t('profile.saved') : t('profile.error') }}
      </p>
      <button class="pq-button" type="submit" :disabled="saving">
        {{ saving ? t('profile.saving') : t('profile.submit') }}
      </button>
    </form>
  </section>
</template>
