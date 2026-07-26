<script setup lang="ts">
import { computed, ref, useRoute } from '#imports'
import { localizeUrl, splitLocalePath } from '../../f36-i18n/src/routing.ts'
import { ADMIN_NAV_ITEMS } from '../components/nav.ts'
import {
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

const route = useRoute()
const session = useAdminSessionState()
const { locale, t } = useAdminI18n()
const menuOpen = ref(false)

const home = computed(() => localizeUrl(
  locale.value as 'de' | 'en' | 'es' | 'fr' | 'it',
  '/app/home',
))
const currentPath = computed(() => splitLocalePath(route.path).pathname)
const currentItem = computed(() => ADMIN_NAV_ITEMS.find(item =>
  item.path === currentPath.value) ?? ADMIN_NAV_ITEMS[0])

function localePath(path: string): string {
  return localizeUrl(locale.value as 'de' | 'en' | 'es' | 'fr' | 'it', path)
}

function closeMenu(): void {
  menuOpen.value = false
}
</script>

<template>
  <div class="admin-shell">
    <a
      class="pq-skip-link"
      href="#admin-main"
    >{{ t('shell.skip') }}</a>

    <aside
      class="admin-sidebar"
      :data-open="menuOpen"
      @keydown.esc="closeMenu"
    >
      <div class="admin-sidebar__brand">
        <a
          class="admin-brand"
          :href="home"
          aria-label="Postqron"
        >
          <img
            src="/brand/logo-reversed.svg"
            alt="Postqron"
          >
          <strong>{{ t('shell.console') }}</strong>
        </a>
        <button
          class="admin-sidebar__close"
          type="button"
          :aria-label="t('shell.closeMenu')"
          @click="closeMenu"
        >
          ×
        </button>
      </div>

      <nav :aria-label="t('shell.navigation')">
        <a
          v-for="item in ADMIN_NAV_ITEMS"
          :key="item.path"
          :href="localePath(item.path)"
          :aria-current="currentPath === item.path ? 'page' : undefined"
          @click="closeMenu"
        >
          <span aria-hidden="true">{{ item.icon }}</span>
          {{ t(item.labelKey) }}
        </a>
      </nav>
    </aside>

    <div
      v-if="menuOpen"
      class="admin-shell__scrim"
      aria-hidden="true"
      @click="closeMenu"
    />

    <div class="admin-shell__body">
      <header class="admin-topbar">
        <button
          class="admin-topbar__menu"
          type="button"
          :aria-label="t('shell.menu')"
          :aria-expanded="menuOpen"
          @click="menuOpen = true"
        >
          ☰
        </button>

        <p class="admin-topbar__section">
          {{ t(currentItem.labelKey) }}
        </p>

        <AdminLanguageSelect />

        <div
          class="admin-topbar__actions"
          data-postqron-slot="admin-topbar-actions"
        >
          <a
            class="admin-identity"
            :href="localePath('/admin/profile')"
            :title="t('shell.profile')"
          >
            {{ session?.account.email }}
          </a>
          <div data-postqron-slot="admin-logout-action" />
        </div>
      </header>

      <main
        id="admin-main"
        class="admin-main"
        tabindex="-1"
        :data-route="route.path"
      >
        <slot />
      </main>
    </div>
  </div>
</template>
