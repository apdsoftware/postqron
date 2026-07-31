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
  useAppWorkspaceTransitionRevisionState,
  useAppWorkspaceTransitionState,
} from '../components/core/use-app-shell.ts'

const route = useRoute()
const session = useAppSessionState()
const bootstrap = useAppBootstrapState()
const accountArea = useAppAccountAreaState()
const api = useAppShellApi()
const workspaceTransition = useAppWorkspaceTransitionState()
const workspaceTransitionRevision = useAppWorkspaceTransitionRevisionState()
const { t } = useAppShellI18n()
const menuOpen = ref(false)
const changingWorkspace = ref(false)
const workspaceSwitchNotice = ref<'restored' | 'unchanged'>()
const workspaceRecoveryUnavailable = ref(false)
const loggingOut = ref(false)
const logoutError = ref(false)
const currentWorkspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const locale = computed(() => localeFromAppPath(route.fullPath))

const links = computed(() => [
  { key: 'home', href: appRoute(locale.value, 'home') },
  { key: 'publish', href: appRoute(locale.value, 'publish') },
  { key: 'calendar', href: appRoute(locale.value, 'calendar') },
  { key: 'social', href: appRoute(locale.value, 'social-channels') },
  { key: 'profile', href: appRoute(locale.value, 'profile') },
  { key: 'security', href: appRoute(locale.value, 'security') },
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
  const previousSession = session.value
  const previousWorkspaceId = previousSession?.current_workspace?.id ?? ''
  let serverCommitted = false
  changingWorkspace.value = true
  workspaceSwitchNotice.value = undefined
  workspaceRecoveryUnavailable.value = false
  workspaceTransition.value = workspaceId
  try {
    await api.selectWorkspace(workspaceId)
    serverCommitted = true
    const refreshedSession = await api.session()
    if (refreshedSession.current_workspace?.id !== workspaceId) {
      throw new Error('APP_WORKSPACE_SWITCH_NOT_VERIFIED')
    }
    session.value = refreshedSession
    await navigateTo(route.fullPath)
  } catch {
    if (!serverCommitted) {
      // The target POST did not commit, so the previously verified client
      // session remains authoritative and no server rollback is necessary.
      session.value = previousSession
      workspaceSwitchNotice.value = 'unchanged'
    } else {
      try {
        if (!previousWorkspaceId) {
          throw new Error('APP_WORKSPACE_ROLLBACK_TARGET_UNAVAILABLE')
        }
        await api.selectWorkspace(previousWorkspaceId)
        const recoveredSession = await api.session()
        if (recoveredSession.current_workspace?.id !== previousWorkspaceId) {
          throw new Error('APP_WORKSPACE_ROLLBACK_NOT_VERIFIED')
        }
        session.value = recoveredSession
        workspaceSwitchNotice.value = 'restored'
      } catch {
        // The server may still be on the new workspace. Remove every cached
        // source of workspace/role authority until a later session retry.
        bootstrap.value = undefined
        accountArea.value = undefined
        session.value = undefined
        workspaceSwitchNotice.value = undefined
        workspaceRecoveryUnavailable.value = true
      }
    }
  } finally {
    if (!workspaceRecoveryUnavailable.value) {
      workspaceTransition.value = undefined
      workspaceTransitionRevision.value += 1
    }
    changingWorkspace.value = false
  }
}

async function retryWorkspaceRecovery() {
  if (changingWorkspace.value) {
    return
  }
  changingWorkspace.value = true
  workspaceTransition.value = 'authoritative-recovery'
  try {
    const recoveredSession = await api.session()
    session.value = recoveredSession
    workspaceRecoveryUnavailable.value = false
    await navigateTo(route.fullPath)
  } catch {
    bootstrap.value = undefined
    accountArea.value = undefined
    session.value = undefined
    workspaceRecoveryUnavailable.value = true
  } finally {
    if (!workspaceRecoveryUnavailable.value) {
      workspaceTransition.value = undefined
      workspaceTransitionRevision.value += 1
    }
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

      <NuxtLink
        class="product-sidebar__primary"
        :to="appRoute(locale, 'publish')"
        @click="menuOpen = false"
      >
        <span aria-hidden="true">＋</span>
        {{ t('shell.newPost') }}
      </NuxtLink>

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
            :disabled="changingWorkspace || workspaceRecoveryUnavailable"
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
          <small
            v-if="workspaceSwitchNotice"
            class="workspace-switcher__error"
            role="alert"
          >
            {{ t(`shell.workspaceSwitch${workspaceSwitchNotice === 'restored' ? 'Restored' : 'Unchanged'}`) }}
          </small>
        </label>

        <div
          class="product-topbar__actions"
          data-postqron-slot="workspace-actions"
        >
          <NuxtLink
            class="pq-button product-topbar__primary"
            :to="appRoute(locale, 'publish')"
          >
            <span aria-hidden="true">＋</span>
            {{ t('shell.newPost') }}
          </NuxtLink>
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
        <AppState
          v-if="workspaceRecoveryUnavailable"
          kind="unavailable"
          action
          @retry="retryWorkspaceRecovery"
        />
        <slot v-else />
      </main>
    </section>
  </div>
</template>
