<script setup lang="ts">
import {
  navigateTo,
  ref,
} from '#imports'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'
import { normalizeAdminApiError } from '../core/api.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

const api = useAdminApi()
const session = useAdminSessionState()
const { locale, t } = useAdminI18n()
const submitting = ref(false)
const errorCode = ref<string>()

async function logout() {
  if (!session.value || submitting.value) {
    return
  }
  submitting.value = true
  errorCode.value = undefined
  try {
    await api.logout(session.value.csrf_token)
    session.value = undefined
    await navigateTo(`${localizeUrl(
      locale.value as 'en' | 'it' | 'es' | 'fr' | 'de',
      '/admin',
    )}?signed_out=1`)
  }
  catch (error) {
    const normalized = normalizeAdminApiError(error)
    if (normalized.code === 'ADMIN_UNAUTHENTICATED') {
      session.value = undefined
      await navigateTo(`${localizeUrl(
        locale.value as 'en' | 'it' | 'es' | 'fr' | 'de',
        '/admin',
      )}?session_expired=1`)
      return
    }
    errorCode.value = normalized.code
  }
  finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="admin-logout">
    <button
      class="admin-logout__button"
      type="button"
      :disabled="submitting"
      @click="logout"
    >
      {{ submitting ? t('shell.loggingOut') : t('shell.logout') }}
    </button>
    <span
      v-if="errorCode"
      class="admin-logout__error"
      role="alert"
    >
      {{ t(`error.${errorCode}` as never) }}
    </span>
  </div>
</template>
