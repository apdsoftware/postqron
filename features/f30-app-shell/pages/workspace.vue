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
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'
import type { WorkspaceMember } from '../components/core/contracts.ts'

definePageMeta({ layout: 'app-shell' })

const api = useAppShellApi()
const session = useAppSessionState()
const { t } = useAppShellI18n()
const members = ref<WorkspaceMember[]>([])
const pageState = ref<'access-denied' | 'offline'>()

useHead(computed(() => ({
  title: t('documentTitle.workspace'),
})))

const { pending, refresh } = useAsyncData('postqron-workspace-members', async () => {
  try {
    members.value = await api.currentWorkspaceMembers()
    pageState.value = undefined
    return members.value
  } catch (error) {
    members.value = []
    pageState.value = appStateKindFromError(error)
    return []
  }
})

async function retry() {
  await refresh()
}
</script>

<template>
  <AppState
    v-if="pending && members.length === 0"
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
    <p class="app-eyebrow">{{ t('workspace.eyebrow') }}</p>
    <h1>{{ t('workspace.title') }}</h1>
    <p class="app-page__lead">{{ t('workspace.description') }}</p>

    <div class="app-page__grid">
      <article class="app-card">
        <span class="app-card__eyebrow">{{ t('workspace.current') }}</span>
        <strong>{{ session?.current_workspace?.name || t('workspace.none') }}</strong>
        <p>{{ session?.current_workspace?.role || t('workspace.none') }}</p>
      </article>
      <article class="app-card">
        <span class="app-card__eyebrow">{{ t('workspace.all') }}</span>
        <ul class="app-list">
          <li v-for="workspace in session?.workspaces ?? []" :key="workspace.id">
            {{ workspace.name }} · {{ workspace.role }}
          </li>
        </ul>
      </article>
    </div>

    <article class="app-card">
      <span class="app-card__eyebrow">{{ t('workspace.members') }}</span>
      <AppState
        v-if="members.length === 0"
        kind="empty"
      />
      <ul
        v-else
        class="app-provider-list"
      >
        <li v-for="member in members" :key="member.id">
          <div>
            <strong>{{ member.email }}</strong>
            <p>{{ member.role }} · {{ member.status }}</p>
          </div>
        </li>
      </ul>
    </article>
  </section>
</template>
