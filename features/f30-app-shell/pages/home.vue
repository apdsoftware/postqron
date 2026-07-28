<script setup lang="ts">
import {
  ref,
  computed,
  definePageMeta,
  useAsyncData,
  useHead,
} from '#imports'
import { appRoute } from '../components/core/navigation.ts'
import {
  appStateKindFromError,
  useAppAccountAreaState,
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: 'app-shell' })

const session = useAppSessionState()
const accountArea = useAppAccountAreaState()
const api = useAppShellApi()
const { t } = useAppShellI18n()
const pageState = ref<'access-denied' | 'offline'>()

useHead(computed(() => ({
  title: t('documentTitle.home'),
})))

const { pending, refresh } = useAsyncData('postqron-account-home', async () => {
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

const currentPlan = computed(() =>
  accountArea.value?.workspaces.find(item =>
    item.workspace.id === session.value?.current_workspace?.id)?.plan)
const cards = computed(() => [
  { key: 'profile', href: appRoute(session.value?.account.locale ?? 'en', 'profile') },
  { key: 'security', href: appRoute(session.value?.account.locale ?? 'en', 'security') },
  { key: 'providers', href: appRoute(session.value?.account.locale ?? 'en', 'providers') },
  { key: 'plan', href: appRoute(session.value?.account.locale ?? 'en', 'plan') },
  { key: 'workspace', href: appRoute(session.value?.account.locale ?? 'en', 'workspace') },
  { key: 'privacy', href: appRoute(session.value?.account.locale ?? 'en', 'privacy') },
])

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
      {{ t('home.eyebrow') }}
    </p>
    <h1>{{ t('home.welcome', { name: session?.account.display_name || t('shell.profile') }) }}</h1>
    <p class="app-page__lead">
      {{ t('home.description') }}
    </p>

    <div class="app-page__hero-grid">
      <article class="app-card app-card--accent">
        <span class="app-card__eyebrow">{{ t('home.currentWorkspace') }}</span>
        <strong>{{ session?.current_workspace?.name || t('home.noWorkspace') }}</strong>
        <p>{{ t('home.currentRole', { role: session?.current_workspace?.role || 'member' }) }}</p>
      </article>
      <article class="app-card">
        <span class="app-card__eyebrow">{{ t('home.currentPlanLabel') }}</span>
        <strong>{{ currentPlan?.name || t('home.planUnknown') }}</strong>
        <p>{{ currentPlan?.state || t('home.planUnknown') }}</p>
      </article>
      <article class="app-card">
        <span class="app-card__eyebrow">{{ t('home.providerCountLabel') }}</span>
        <strong>{{ accountArea?.providers.length ?? 0 }}</strong>
        <p>{{ t('home.providerCountDescription') }}</p>
      </article>
    </div>

    <div class="app-page__grid">
      <a
        v-for="card in cards"
        :key="card.key"
        class="app-card app-card--link"
        :href="card.href"
      >
        <span class="app-card__eyebrow">{{ t(`home.card.${card.key}.eyebrow`) }}</span>
        <strong>{{ t(`home.card.${card.key}.title`) }}</strong>
        <p>{{ t(`home.card.${card.key}.description`) }}</p>
      </a>
    </div>

    <div
      class="app-home__summary"
      data-postqron-slot="home-summary"
    >
      <article>
        <span aria-hidden="true">◎</span>
        <p>{{ t('home.getStarted') }}</p>
      </article>
    </div>
    <div class="app-home__grid">
      <section
        class="app-slot"
        data-postqron-slot="home-primary"
        :aria-label="t('state.empty.title')"
      >
        <AppState kind="empty" />
      </section>
      <section
        class="app-slot"
        data-postqron-slot="home-secondary"
        :aria-label="t('state.empty.title')"
      >
        <AppState kind="empty" />
      </section>
    </div>
  </section>
</template>
