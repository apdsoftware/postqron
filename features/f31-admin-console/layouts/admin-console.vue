<script setup lang="ts">
import { computed, useRoute } from '#imports'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'
import {
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

const route = useRoute()
const session = useAdminSessionState()
const { locale, t } = useAdminI18n()
const home = computed(() => localizeUrl(
  locale.value as 'en' | 'it' | 'es' | 'fr' | 'de',
  '/app/home',
))
</script>

<template>
  <div class="admin-shell">
    <a
      class="pq-skip-link"
      href="#admin-main"
    >{{ t('shell.skip') }}</a>
    <header class="admin-topbar">
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
      <nav :aria-label="t('shell.navigation')">
        <PostqronLanguageSwitcher />
        <a :href="home">{{ t('shell.back') }}</a>
      </nav>
      <p
        class="admin-identity"
        :title="t('shell.profile')"
      >
        {{ session?.account.email }}
      </p>
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
