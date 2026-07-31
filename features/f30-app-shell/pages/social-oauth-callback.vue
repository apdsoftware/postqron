<script setup lang="ts">
import {
  computed,
  definePageMeta,
  onMounted,
  ref,
  useHead,
  useRoute,
} from '#imports'
import {
  SOCIAL_OAUTH_CALLBACK_PARAMETERS,
  socialCallbackHandoffDocument,
  socialOAuthCallbackInput,
} from '../components/core/social-callback.ts'
import {
  normalizeSocialApiError,
} from '../components/core/social-api.ts'
import {
  useAppShellI18n,
  useSocialConnectionsApi,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: false })

const route = useRoute()
const social = useSocialConnectionsApi()
const { t } = useAppShellI18n()
const handoff = ref<string>()

useHead(computed(() => ({ title: t('documentTitle.callback') })))

onMounted(async () => {
  try {
    const input = socialOAuthCallbackInput(route.query)

    // OAuth response values are captured in memory and removed from browser
    // history before F5 performs the authoritative state/issuer validation.
    const cleanURL = new globalThis.URL(globalThis.location.href)
    for (const parameter of SOCIAL_OAUTH_CALLBACK_PARAMETERS) {
      cleanURL.searchParams.delete(parameter)
    }
    globalThis.history.replaceState(
      globalThis.history.state,
      '',
      `${cleanURL.pathname}${cleanURL.search}${cleanURL.hash}`,
    )

    handoff.value = socialCallbackHandoffDocument(
      await social.completeAuthorization(input),
    )
  } catch (error) {
    handoff.value = socialCallbackHandoffDocument(normalizeSocialApiError(error))
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
    <AppState kind="loading" />
    <pre
      v-if="handoff !== undefined"
      data-postqron-social-callback-handoff
      hidden
    >{{ handoff }}</pre>
  </main>
</template>
