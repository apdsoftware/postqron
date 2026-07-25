<script setup lang="ts">
import {
  computed,
  navigateTo,
  ref,
  useRoute,
} from '#imports'
import {
  appRoot,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import {
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

const route = useRoute()
const session = useAppSessionState()
const api = useAppShellApi()
const { t } = useAppShellI18n()
const menuOpen = ref(false)
const changingWorkspace = ref(false)
const currentWorkspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const locale = computed(() => localeFromAppPath(route.fullPath))
const home = computed(() => `${appRoot(locale.value)}/home`)

async function selectWorkspace(event: unknown) {
  const workspaceId = (
    event as { target?: { value?: string } }
  ).target?.value ?? ''
  if (!workspaceId || workspaceId === currentWorkspaceId.value) {
    return
  }
  changingWorkspace.value = true
  try {
    await api.selectWorkspace(workspaceId)
    session.value = await api.session()
    await navigateTo(home.value)
  } finally {
    changingWorkspace.value = false
  }
}

async function logout() {
  try {
    await api.logout()
  } finally {
    session.value = undefined
    await navigateTo(appRoot(locale.value))
  }
}
</script>

<template>
  <div class="product-shell">
    <a
      class="pq-skip-link"
      href="#app-main"
    >{{ t('shell.skip') }}</a>

    <aside
      class="product-sidebar"
      :data-open="menuOpen"
    >
      <div class="product-sidebar__brand">
        <a
          :href="home"
          aria-label="Postqron"
        >
          <img
            src="/brand/logo-reversed.svg"
            alt="Postqron"
          >
        </a>
        <button
          class="product-sidebar__close"
          type="button"
          :aria-label="t('shell.closeMenu')"
          @click="menuOpen = false"
        >
          ×
        </button>
      </div>

      <nav :aria-label="t('shell.navigation')">
        <a
          :href="home"
          :aria-current="route.path === home ? 'page' : undefined"
          @click="menuOpen = false"
        >
          <span aria-hidden="true">⌂</span>
          {{ t('shell.home') }}
        </a>
        <div data-postqron-slot="primary-navigation" />
      </nav>
    </aside>

    <div
      v-if="menuOpen"
      class="product-shell__scrim"
      aria-hidden="true"
      @click="menuOpen = false"
    />

    <section class="product-shell__body">
      <header class="product-topbar">
        <button
          class="product-topbar__menu"
          type="button"
          :aria-label="t('shell.menu')"
          :aria-expanded="menuOpen"
          @click="menuOpen = true"
        >
          ☰
        </button>

        <label class="workspace-switcher">
          <span>{{ t('shell.workspace') }}</span>
          <select
            :value="currentWorkspaceId"
            :disabled="changingWorkspace"
            @change="selectWorkspace"
          >
            <option
              v-for="workspace in session?.workspaces ?? []"
              :key="workspace.id"
              :value="workspace.id"
            >
              {{ workspace.name }}
            </option>
          </select>
        </label>

        <div
          class="product-topbar__actions"
          data-postqron-slot="workspace-actions"
        />

        <details class="profile-menu">
          <summary>
            <span
              class="profile-menu__avatar"
              aria-hidden="true"
            >
              {{ session?.account.display_name?.slice(0, 1).toUpperCase() || 'P' }}
            </span>
            <span class="profile-menu__name">
              {{ session?.account.display_name || t('shell.profile') }}
            </span>
          </summary>
          <div class="profile-menu__panel">
            <strong>{{ session?.account.display_name }}</strong>
            <small>{{ session?.account.email }}</small>
            <PostqronLanguageSwitcher />
            <button
              type="button"
              @click="logout"
            >
              {{ t('shell.logout') }}
            </button>
          </div>
        </details>
      </header>

      <main
        id="app-main"
        class="product-main"
        tabindex="-1"
      >
        <slot />
      </main>
    </section>
  </div>
</template>
