<script setup lang="ts">
import {
  computed,
  navigateTo,
  ref,
  useRoute,
} from '#imports'
import { normalizeAppApiError } from '../components/core/api.ts'
import {
  appRoute,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import {
  useAppAccountAreaState,
  useAppBootstrapState,
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

const route = useRoute()
const session = useAppSessionState()
const bootstrap = useAppBootstrapState()
const accountArea = useAppAccountAreaState()
const api = useAppShellApi()
const { t } = useAppShellI18n()
const menuOpen = ref(false)
const changingWorkspace = ref(false)
const loggingOut = ref(false)
const logoutError = ref(false)
const currentWorkspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const locale = computed(() => localeFromAppPath(route.fullPath))

const links = computed(() => [
  { key: 'home', href: appRoute(locale.value, 'home') },
  { key: 'profile', href: appRoute(locale.value, 'profile') },
  { key: 'security', href: appRoute(locale.value, 'security') },
  { key: 'providers', href: appRoute(locale.value, 'providers') },
  { key: 'social', href: appRoute(locale.value, 'social-channels') },
  { key: 'plan', href: appRoute(locale.value, 'plan') },
  { key: 'workspace', href: appRoute(locale.value, 'workspace') },
  { key: 'privacy', href: appRoute(locale.value, 'privacy') },
])

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
    await navigateTo(route.fullPath)
  } finally {
    changingWorkspace.value = false
  }
}

async function logout() {
  if (loggingOut.value) {
    return
  }
  loggingOut.value = true
  logoutError.value = false
  try {
    await api.logout()
  } catch (error) {
    if (normalizeAppApiError(error).kind !== 'session') {
      logoutError.value = true
      return
    }
  } finally {
    loggingOut.value = false
  }

  bootstrap.value = undefined
  accountArea.value = undefined
  session.value = undefined
  const entry = appRoute(locale.value, 'entry')
  if (import.meta.client) {
    globalThis.location.replace(entry)
  } else {
    await navigateTo(entry, { external: true, replace: true })
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
        <NuxtLink
          :to="appRoute(locale, 'home')"
          aria-label="Postqron"
        >
          <img
            src="/brand/logo-reversed.svg"
            alt="Postqron"
          >
        </NuxtLink>
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
        <NuxtLink
          v-for="link in links"
          :key="link.key"
          :to="link.href"
          :aria-current="route.path === link.href ? 'page' : undefined"
          @click="menuOpen = false"
        >
          {{ t(`shell.nav.${link.key}`) }}
        </NuxtLink>
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
        >
          <PostqronLanguageSwitcher />
        </div>

        <details class="profile-menu">
          <summary
            :aria-label="`${t('shell.profile')}: ${session?.account.display_name || t('shell.profile')}`"
          >
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
            <div class="profile-menu__identity">
              <strong>{{ session?.account.display_name }}</strong>
              <small>{{ session?.account.email }}</small>
            </div>
            <NuxtLink
              class="profile-menu__link"
              :to="appRoute(locale, 'profile')"
            >
              {{ t('shell.profile') }}
            </NuxtLink>
            <button
              class="profile-menu__logout"
              type="button"
              :disabled="loggingOut"
              @click="logout"
            >
              {{ t('shell.logout') }}
            </button>
            <p
              v-if="logoutError"
              class="profile-menu__logout-error"
              role="alert"
            >
              {{ t('shell.logoutError') }}
            </p>
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
