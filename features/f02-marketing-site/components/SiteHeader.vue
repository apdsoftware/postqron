<script setup lang="ts">
import { useRoute, useRuntimeConfig } from '#imports'
import PqLogo from '../../f01-brand/components/PqLogo.vue'

const config = useRuntimeConfig()
const route = useRoute()
const navigation = [
  { label: 'Funzionalità', to: '/funzionalita' },
  { label: 'Prezzi', to: '/prezzi' },
  { label: 'FAQ', to: '/faq' },
]

const isCurrent = (to: string) => route.path === to
</script>

<template>
  <header class="site-header">
    <div class="site-header__inner content-wrap">
      <NuxtLink
        class="site-header__brand"
        to="/"
        aria-label="Postqron, home"
      >
        <PqLogo />
      </NuxtLink>

      <nav
        class="site-header__desktop"
        aria-label="Navigazione principale"
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

      <a
        class="pq-button site-header__cta"
        :href="config.public.appUrl"
      >
        Inizia ora
      </a>

      <details class="site-header__mobile">
        <summary aria-label="Apri il menu">
          <span aria-hidden="true" />
          <span aria-hidden="true" />
          <span aria-hidden="true" />
        </summary>
        <nav aria-label="Navigazione mobile">
          <NuxtLink
            v-for="item in navigation"
            :key="item.to"
            :to="item.to"
            :aria-current="isCurrent(item.to) ? 'page' : undefined"
          >
            {{ item.label }}
          </NuxtLink>
          <a :href="config.public.appUrl">Inizia ora</a>
        </nav>
      </details>
    </div>
  </header>
</template>
