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
import {
  formatDateTime,
  localeOptions,
  timezoneGroups,
} from '../components/core/preferences.ts'

definePageMeta({ layout: 'app-shell' })

const api = useAppShellApi()
const accountArea = useAppAccountAreaState()
const session = useAppSessionState()
const { t, locale: uiLocale } = useAppShellI18n()
const saving = ref(false)
const feedback = ref<'error' | 'saved'>()
const displayName = ref('')
const localeValue = ref('it-IT')
const timezone = ref('Europe/Rome')
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()

useHead(computed(() => ({
  title: t('documentTitle.profile'),
})))

const { pending, refresh } = useAsyncData('postqron-account-profile', async () => {
  try {
    accountArea.value = await api.accountArea()
    displayName.value = accountArea.value.profile.display_name
    localeValue.value = accountArea.value.profile.locale
    timezone.value = accountArea.value.profile.timezone
    pageState.value = undefined
    return accountArea.value
  } catch (error) {
    accountArea.value = undefined
    pageState.value = appStateKindFromError(error)
    return undefined
  }
}, { server: false })

const localeChoices = computed(() => localeOptions(localeValue.value))
const timezoneChoices = computed(() => timezoneGroups(timezone.value))
const emailVerified = computed(() => session.value?.account.email_verified ?? false)
const updatedAtLabel = computed(() =>
  accountArea.value
    ? formatDateTime(accountArea.value.profile.updated_at, uiLocale.value)
    : '')

async function saveProfile() {
  saving.value = true
  feedback.value = undefined
  try {
    const profile = await api.updateProfile({
      displayName: displayName.value,
      locale: localeValue.value,
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
    <p class="app-eyebrow">
      {{ t('profile.eyebrow') }}
    </p>
    <h1>{{ t('profile.title') }}</h1>
    <p class="app-page__lead">
      {{ t('profile.description') }}
    </p>

    <div class="app-page__stack">
      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('profile.detailsSection') }}</span>
          <h2>{{ t('profile.detailsTitle') }}</h2>
        </div>
        <form
          class="app-form-grid"
          @submit.prevent="saveProfile"
        >
          <label class="app-field">
            <span>{{ t('profile.displayName') }}</span>
            <input
              v-model="displayName"
              type="text"
              maxlength="100"
              required
              autocomplete="name"
            >
          </label>
          <label class="app-field">
            <span>{{ t('profile.email') }}</span>
            <input
              :value="session?.account.email || ''"
              type="email"
              readonly
              aria-describedby="profile-email-help"
            >
            <span
              id="profile-email-help"
              class="app-field__help"
            >{{ t('profile.emailReadonly') }}</span>
          </label>
          <label class="app-field">
            <span>{{ t('profile.locale') }}</span>
            <select
              v-model="localeValue"
              required
              aria-describedby="profile-locale-help"
            >
              <option
                v-for="option in localeChoices"
                :key="option.value"
                :value="option.value"
              >
                {{ option.label }}
              </option>
            </select>
            <span
              id="profile-locale-help"
              class="app-field__help"
            >{{ t('profile.localeHelp') }}</span>
          </label>
          <label class="app-field">
            <span>{{ t('profile.timezone') }}</span>
            <select
              v-model="timezone"
              required
              aria-describedby="profile-timezone-help"
            >
              <optgroup
                v-for="group in timezoneChoices"
                :key="group.region"
                :label="group.region"
              >
                <option
                  v-for="zone in group.zones"
                  :key="zone"
                  :value="zone"
                >
                  {{ zone }}
                </option>
              </optgroup>
            </select>
            <span
              id="profile-timezone-help"
              class="app-field__help"
            >{{ t('profile.timezoneHelp') }}</span>
          </label>
          <p
            v-if="feedback"
            class="app-inline-alert"
            :data-success="feedback === 'saved'"
            role="status"
          >
            {{ feedback === 'saved' ? t('profile.saved') : t('profile.error') }}
          </p>
          <button
            class="pq-button"
            type="submit"
            :disabled="saving"
          >
            {{ saving ? t('profile.saving') : t('profile.submit') }}
          </button>
        </form>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('profile.statusSection') }}</span>
          <h2>{{ t('profile.statusTitle') }}</h2>
        </div>
        <dl class="app-detail-list">
          <div class="app-inline-meta">
            <dt>{{ t('profile.updatedAt') }}</dt>
            <dd>{{ updatedAtLabel }}</dd>
          </div>
          <div class="app-inline-meta">
            <dt>{{ t('profile.emailStatus') }}</dt>
            <dd>
              <span
                class="app-badge"
                :class="emailVerified ? 'app-badge--success' : 'app-badge--warning'"
              >
                {{ emailVerified ? t('profile.verified') : t('profile.unverified') }}
              </span>
            </dd>
          </div>
        </dl>
      </article>
    </div>
  </section>
</template>
