<script setup lang="ts">
import { useFetch, useRuntimeConfig } from '#imports'

const config = useRuntimeConfig()
const { data: health } = await useFetch('/api/health')
</script>

<template>
  <div class="app-shell">
    <header class="site-header">
      <NuxtLink
        class="brand"
        to="/"
      >
        Postqron
      </NuxtLink>
      <span
        class="status"
        aria-live="polite"
      >
        {{ health?.status === 'ok' ? 'Shell operativo' : 'Verifica in corso' }}
      </span>
    </header>

    <main class="hero">
      <p class="eyebrow">
        Il tuo spazio per i contenuti social
      </p>
      <h1>Programma e gestisci i tuoi contenuti social da un unico spazio.</h1>
      <p class="intro">
        La fondazione applicativa è pronta. Le funzionalità vengono caricate come
        slice autonome, con contratti e dipendenze dichiarati vicino al codice.
      </p>
      <div class="actions">
        <a
          class="primary-action"
          :href="`${config.public.apiBase}/healthz`"
        >
          Verifica API
        </a>
        <NuxtLink
          class="secondary-action"
          to="/api/features"
        >
          Feature caricate
        </NuxtLink>
      </div>
    </main>

    <footer class="site-footer">
      Sviluppato da
      <a
        href="https://apdsoftware.it"
        rel="noopener noreferrer"
      >
        APDSoftware
      </a>
    </footer>
  </div>
</template>
