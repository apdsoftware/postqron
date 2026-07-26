<script setup lang="ts">
import { ref } from '#imports'
import { AdminApiError, normalizeAdminApiError } from '../core/api.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

const api = useAdminApi()
const session = useAdminSessionState()
const { t } = useAdminI18n()
const emit = defineEmits<{ authenticated: [] }>()

const email = ref('')
const password = ref('')
const authenticating = ref(false)
const errorCode = ref<AdminApiError['code']>()

async function login() {
  authenticating.value = true
  errorCode.value = undefined
  try {
    await api.passwordLogin({
      email: email.value,
      password: password.value,
    })
    password.value = ''
    session.value = await api.session()
    emit('authenticated')
  } catch (error) {
    password.value = ''
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    authenticating.value = false
  }
}
</script>

<template>
  <section
    class="admin-panel admin-login"
    aria-labelledby="admin-login-title"
  >
    <h2 id="admin-login-title">
      {{ t('login.title') }}
    </h2>
    <p>{{ t('login.description') }}</p>
    <form @submit.prevent="login">
      <label for="admin-email">{{ t('login.email') }}</label>
      <input
        id="admin-email"
        v-model="email"
        type="email"
        autocomplete="username"
        maxlength="320"
        required
      >
      <label for="admin-password">{{ t('login.password') }}</label>
      <input
        id="admin-password"
        v-model="password"
        type="password"
        autocomplete="current-password"
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
      <button
        class="pq-button pq-button--primary"
        type="submit"
        :disabled="authenticating"
      >
        {{ authenticating ? t('login.signingIn') : t('login.submit') }}
      </button>
    </form>
  </section>
</template>
