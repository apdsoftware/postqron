<script setup lang="ts">
import {
  computed,
  ref,
  useRoute,
} from '#imports'
import {
  ADMIN_NAV_ITEMS,
  isAdminNavItemActive,
} from '../components/admin-nav.ts'
import { AdminApiError, normalizeAdminApiError } from '../core/api.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'

const route = useRoute()
const session = useAdminSessionState()
const api = useAdminApi()
const { locale, t } = useAdminI18n()
const menuOpen = ref(false)
const authenticating = ref(false)
const email = ref('')
const password = ref('')
const errorCode = ref<AdminApiError['code']>()

const home = computed(() => localizeUrl(
  locale.value as 'en' | 'it' | 'es' | 'fr' | 'de',
  '/app/home',
))

const navItems = computed(() => ADMIN_NAV_ITEMS.map(item => ({
  ...item,
  href: localizeUrl(locale.value as 'en' | 'it' | 'es' | 'fr' | 'de', item.path),
  active: isAdminNavItemActive(item, route.path),
})))

const currentSectionLabel = computed(() => {
  const active = navItems.value.find(item => item.active)
  return active ? t(active.labelKey) : t('nav.dashboard')
})

async function login() {
  authenticating.value = true
  errorCode.value = undefined
  try {
    await api.passwordLogin({
      email: email.value,
      password: password.value,
    })
    password.value = ''
    session.value = await api.session()
  } catch (error) {
    password.value = ''
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    authenticating.value = false
  }
}

function logout() {
  session.value = undefined
  menuOpen.value = false
}
</script>

<template>
  <div
    class="admin-shell"
    @keydown.esc="menuOpen = false"
  >
    <a
      class="pq-skip-link"
      href="#admin-main"
    >{{ t('shell.skip') }}</a>

    <template v-if="session">
      <aside
        class="admin-sidebar"
        :data-open="menuOpen"
      >
        <div class="admin-sidebar__brand">
          <a
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
            @click="menuOpen = false"
          >
            ×
          </button>
        </div>
        <nav :aria-label="t('shell.navigation')">
          <a
            v-for="item in navItems"
            :key="item.path"
            :href="item.href"
            :aria-current="item.active ? 'page' : undefined"
            @click="menuOpen = false"
          >
            <span aria-hidden="true">{{ item.icon }}</span>
            {{ t(item.labelKey) }}
          </a>
        </nav>
        <a
          class="admin-sidebar__back"
          :href="home"
        >{{ t('shell.back') }}</a>
      </aside>

      <div
        v-if="menuOpen"
        class="admin-shell__scrim"
        aria-hidden="true"
        @click="menuOpen = false"
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
          <p
            class="admin-page-title"
            aria-live="polite"
          >
            {{ currentSectionLabel }}
          </p>
          <details class="admin-profile-menu">
            <summary>
              <span
                class="admin-profile-menu__avatar"
                aria-hidden="true"
              >{{ session.account.email.slice(0, 1).toUpperCase() }}</span>
              <span
                class="admin-profile-menu__name"
                :title="t('shell.profile')"
              >{{ session.account.email }}</span>
            </summary>
            <div class="admin-profile-menu__panel">
              <strong>{{ t('shell.profile') }}</strong>
              <small>{{ session.account.email }}</small>
              <PostqronLanguageSwitcher />
              <a :href="localizeUrl(locale as 'en' | 'it' | 'es' | 'fr' | 'de', '/admin/profile')">
                {{ t('nav.profile') }}
              </a>
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
          id="admin-main"
          class="admin-main"
          tabindex="-1"
          :data-route="route.path"
        >
          <slot />
        </main>
      </div>
    </template>

    <main
      v-else
      id="admin-main"
      class="admin-main admin-main--login"
      tabindex="-1"
    >
      <header class="admin-page__heading">
        <p class="admin-eyebrow">
          {{ t('page.eyebrow') }}
        </p>
        <h1>{{ t('page.title') }}</h1>
        <p>{{ t('page.description') }}</p>
      </header>
      <section
        class="admin-panel admin-login"
        aria-labelledby="admin-login-title"
      >
        <h2 id="admin-login-title">
          {{ t('login.title') }}
        </h2>
        <p>{{ t('login.description') }}</p>
        <form @submit.prevent="login">
          <label for="admin-email">{{ t('login.email') }}</label>
          <input
            id="admin-email"
            v-model="email"
            type="email"
            autocomplete="username"
            maxlength="320"
            required
          >
          <label for="admin-password">{{ t('login.password') }}</label>
          <input
            id="admin-password"
            v-model="password"
            type="password"
            autocomplete="current-password"
            minlength="12"
            maxlength="1024"
            required
          >
          <p
            v-if="errorCode"
            class="admin-inline-error"
            role="alert"
          >
            {{ t(`error.${errorCode}` as never) }}
          </p>
          <button
            class="pq-button pq-button--primary"
            type="submit"
            :disabled="authenticating"
          >
            {{ authenticating ? t('login.signingIn') : t('login.submit') }}
          </button>
        </form>
      </section>
    </main>
  </div>
</template>
