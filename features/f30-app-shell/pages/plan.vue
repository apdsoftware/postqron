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
const session = useAppSessionState()
const accountArea = useAppAccountAreaState()
const { t } = useAppShellI18n()
const pageState = ref<'access-denied' | 'offline'>()

useHead(computed(() => ({
  title: t('documentTitle.plan'),
})))

const { pending, refresh } = useAsyncData('postqron-account-plan', async () => {
  try {
    accountArea.value = await api.accountArea()
    pageState.value = undefined
    return accountArea.value
  } catch (error) {
    accountArea.value = undefined
    pageState.value = appStateKindFromError(error)
    return undefined
  }
})

const workspacePlan = computed(() =>
  accountArea.value?.workspaces.find(item =>
    item.workspace.id === session.value?.current_workspace?.id))

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
  <AppState
    v-else-if="!workspacePlan"
    kind="empty"
  />
  <section
    v-else
    class="app-page"
  >
    <p class="app-eyebrow">
      {{ t('plan.eyebrow') }}
    </p>
    <h1>{{ t('plan.title') }}</h1>
    <p class="app-page__lead">
      {{ t('plan.description') }}
    </p>

    <article class="app-card">
      <span class="app-card__eyebrow">{{ t('plan.current') }}</span>
      <strong>{{ workspacePlan?.plan.name || t('plan.unknown') }}</strong>
      <p>{{ workspacePlan?.plan.state || t('plan.unknown') }}</p>
    </article>

    <div class="app-page__grid">
      <article class="app-card">
        <span class="app-card__eyebrow">{{ t('plan.usage') }}</span>
        <ul class="app-list">
          <li
            v-for="(value, key) in workspacePlan?.plan.usage ?? {}"
            :key="key"
          >
            {{ key }}: {{ value }}
          </li>
        </ul>
      </article>
      <article class="app-card">
        <span class="app-card__eyebrow">{{ t('plan.limits') }}</span>
        <ul class="app-list">
          <li
            v-for="(value, key) in workspacePlan?.plan.limits ?? {}"
            :key="key"
          >
            {{ key }}: {{ value }}
          </li>
        </ul>
      </article>
    </div>
  </section>
</template>
