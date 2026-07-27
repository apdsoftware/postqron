<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useHead,
} from '#imports'
import AdminPageHeader from '../components/AdminPageHeader.vue'
import { normalizeAdminApiError } from '../core/api.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const session = useAdminSessionState()
const api = useAdminApi()
const { date, t } = useAdminI18n()
const currentPassword = ref('')
const newPassword = ref('')
const confirmation = ref('')
const submitting = ref(false)
const changed = ref(false)
const errorCode = ref<string>()

useHead(computed(() => ({
  title: t('document.title'),
})))

function clearPasswords() {
  currentPassword.value = ''
  newPassword.value = ''
  confirmation.value = ''
}

async function changePassword() {
  if (!session.value || submitting.value) {
    return
  }
  changed.value = false
  errorCode.value = undefined
  if (newPassword.value !== confirmation.value) {
    errorCode.value = 'ADMIN_PASSWORD_CONFIRMATION_MISMATCH'
    clearPasswords()
    return
  }
  submitting.value = true
  try {
    await api.changePassword({
      confirmation: confirmation.value,
      csrfToken: session.value.csrf_token,
      currentPassword: currentPassword.value,
      newPassword: newPassword.value,
    })
    clearPasswords()
    session.value = await api.session()
    changed.value = true
  }
  catch (error) {
    clearPasswords()
    errorCode.value = normalizeAdminApiError(error).code
  }
  finally {
    submitting.value = false
  }
}
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('profile.title')"
      :description="t('profile.description')"
    />

    <section
      class="admin-panel"
      aria-labelledby="admin-profile-title"
    >
      <h2 id="admin-profile-title">
        {{ t('shell.profile') }}
      </h2>
      <dl class="admin-profile-details">
        <dt>{{ t('profile.accountId') }}</dt>
        <dd>{{ session?.account.id }}</dd>
        <dt>{{ t('profile.email') }}</dt>
        <dd>{{ session?.account.email }}</dd>
        <dt>{{ t('profile.authenticatedAt') }}</dt>
        <dd>
          <time :datetime="session?.authenticated_at">{{ session ? date(session.authenticated_at) : '' }}</time>
        </dd>
      </dl>
      <p class="admin-profile-hint">
        {{ t('profile.logoutHint') }}
      </p>
    </section>

    <section
      class="admin-panel admin-password"
      aria-labelledby="admin-password-title"
    >
      <h2 id="admin-password-title">
        {{ t('profile.password.title') }}
      </h2>
      <p id="admin-password-policy">
        {{ t('profile.password.policy') }}
      </p>
      <form @submit.prevent="changePassword">
        <label for="admin-current-password">
          {{ t('profile.password.current') }}
        </label>
        <input
          id="admin-current-password"
          v-model="currentPassword"
          type="password"
          autocomplete="current-password"
          minlength="12"
          maxlength="1024"
          required
        >
        <label for="admin-new-password">
          {{ t('profile.password.new') }}
        </label>
        <input
          id="admin-new-password"
          v-model="newPassword"
          type="password"
          autocomplete="new-password"
          minlength="12"
          maxlength="1024"
          aria-describedby="admin-password-policy"
          required
        >
        <label for="admin-password-confirmation">
          {{ t('profile.password.confirm') }}
        </label>
        <input
          id="admin-password-confirmation"
          v-model="confirmation"
          type="password"
          autocomplete="new-password"
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
        <p
          v-if="changed"
          class="admin-inline-success"
          role="status"
        >
          {{ t('profile.password.success') }}
        </p>
        <button
          class="pq-button pq-button--primary"
          type="submit"
          :disabled="submitting"
        >
          {{
            submitting
              ? t('profile.password.saving')
              : t('profile.password.submit')
          }}
        </button>
      </form>
    </section>
  </section>
</template>
