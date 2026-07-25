<script setup lang="ts">
import { computed, useRoute, useRuntimeConfig } from '#imports'
import PqLogo from '../../f01-brand/components/PqLogo.vue'
import { useMarketingSiteI18n } from '../locales/runtime.ts'

const config = useRuntimeConfig()
const route = useRoute()
const i18n = useMarketingSiteI18n()

const navigation = computed(() => [
  { label: i18n.translate('marketing-nav.links.features'), to: i18n.localize('/funzionalita') },
  { label: i18n.translate('marketing-nav.links.pricing'), to: i18n.localize('/prezzi') },
  { label: i18n.translate('marketing-nav.links.faq'), to: i18n.localize('/faq') },
])

const isCurrent = (to: string) => route.path === to
</script>

<template>
  <header class="site-header">
    <div class="site-header__inner content-wrap">
      <NuxtLink
        class="site-header__brand"
        :to="i18n.localize('/')"
        :aria-label="i18n.translate('marketing-nav.brand.homeLabel')"
      >
        <PqLogo />
      </NuxtLink>

      <nav
        class="site-header__desktop"
        :aria-label="i18n.translate('marketing-nav.nav.primaryLabel')"
      >
        <NuxtLink
          v-for="item in navigation"
          :key="item.to"
          :to="item.to"
          :aria-current="isCurrent(item.to) ? 'page' : undefined"
        >
          {{ item.label }}
        </NuxtLink>
      </nav>

      <PostqronLanguageSwitcher class="site-header__language" />

      <a
        class="pq-button site-header__cta"
        :href="config.public.appUrl"
      >
        {{ i18n.translate('marketing-nav.cta.start') }}
      </a>

      <details class="site-header__mobile">
        <summary :aria-label="i18n.translate('marketing-nav.menu.openLabel')">
          <span aria-hidden="true" />
          <span aria-hidden="true" />
          <span aria-hidden="true" />
        </summary>
        <nav :aria-label="i18n.translate('marketing-nav.nav.mobileLabel')">
          <NuxtLink
            v-for="item in navigation"
            :key="item.to"
            :to="item.to"
            :aria-current="isCurrent(item.to) ? 'page' : undefined"
          >
            {{ item.label }}
          </NuxtLink>
          <a :href="config.public.appUrl">{{ i18n.translate('marketing-nav.cta.start') }}</a>
          <PostqronLanguageSwitcher />
        </nav>
      </details>
    </div>
  </header>
</template>
