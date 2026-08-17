<script setup lang="ts">
// Placeholder dello scaffold: il design system Hexagon arriva con la issue #401
// (backlog 19), bloccata da Q1 — i file del template non sono nel repository.
const { public: config } = useRuntimeConfig()

useHead({
  title: 'Cronjob affidabili, definiti come codice',
  link: [{ rel: 'canonical', href: canonicalUrl('/', config.siteUrl) }],
})
</script>

<template>
  <main class="hero">
    <h1>PostQron</h1>
    <p>
      Scheduling affidabile dei tuoi cronjob, senza gestire un server cron.
      I job vivono come codice nel tuo repository GitHub.
    </p>
    <p class="note">
      Scaffold del monorepo. Il design system Hexagon verrà integrato con la
      issue dedicata del sito pubblico.
    </p>
  </main>
</template>

<style scoped>
.hero {
  margin: 0 auto;
  max-width: 42rem;
  padding: 6rem 1.5rem;
  font-family: system-ui, sans-serif;
  line-height: 1.6;
}

.note {
  color: #666;
  font-size: 0.875rem;
}
</style>
