<script setup lang="ts">
import {
  computed,
  definePageMeta,
  navigateTo,
  onMounted,
  ref,
  useHead,
  useRoute,
} from '#imports'
import {
  appRoot,
  authenticatedDestination,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import {
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: false })

const route = useRoute()
const api = useAppShellApi()
const { t } = useAppShellI18n()
const failed = ref(false)
const locale = localeFromAppPath(route.fullPath)

useHead(computed(() => ({
  title: t('documentTitle.callback'),
})))

function queryString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

onMounted(async () => {
  try {
    const result = await api.callback({
      state: queryString(route.query.state),
      code: queryString(route.query.code),
      error: queryString(route.query.error),
    })
    await navigateTo(
      authenticatedDestination(result.returnTo, result.onboarding),
      { replace: true },
    )
  } catch {
    failed.value = true
  }
})
</script>

<template>
  <main class="auth-callback">
    <img
      src="/brand/mark.svg"
      alt=""
      aria-hidden="true"
    >
    <AppState
      v-if="!failed"
      kind="loading"
    />
    <section
      v-else
      class="app-state app-state--access-denied"
      role="alert"
    >
      <span
        class="app-state__icon"
        aria-hidden="true"
      >!</span>
      <div>
        <h1>{{ t('callback.title') }}</h1>
        <p>{{ t('callback.description') }}</p>
        <a
          class="pq-button"
          :href="appRoot(locale)"
        >{{ t('callback.retry') }}</a>
      </div>
    </section>
  </main>
</template>
