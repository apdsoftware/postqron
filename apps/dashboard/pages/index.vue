<script setup lang="ts">
// Placeholder dello scaffold: il layout Flowbite e la navigazione arrivano con
// la issue dedicata alla dashboard cliente (backlog 24).
//
// La chiamata all'health check è volutamente lato client: la dashboard è una SPA
// statica e ogni dato dinamico passa dal backend Go.
const { public: config } = useRuntimeConfig()

type Health = { status: string, env: string, version: string }

const health = ref<Health | null>(null)
const error = ref<string | null>(null)
const pending = ref(false)

async function checkHealth() {
  pending.value = true
  error.value = null
  try {
    health.value = await $fetch<Health>(apiUrl('/healthz', config.apiBaseUrl))
  }
  catch (cause) {
    health.value = null
    error.value = cause instanceof Error ? cause.message : 'Backend non raggiungibile'
  }
  finally {
    pending.value = false
  }
}

useHead({ title: 'Dashboard' })
</script>

<template>
  <main class="shell">
    <h1>PostQron · Dashboard</h1>
    <p>
      Scaffold del monorepo. Il template Flowbite, l'autenticazione e la gestione
      dei cronjob arrivano con le issue dedicate.
    </p>

    <section>
      <h2>Backend</h2>
      <p class="mono">
        API: {{ config.apiBaseUrl }}
      </p>
      <button
        type="button"
        :disabled="pending"
        @click="checkHealth"
      >
        {{ pending ? 'Verifico…' : 'Verifica health check' }}
      </button>
      <p
        v-if="health"
        class="mono ok"
      >
        {{ health.status }} · {{ health.env }} · {{ health.version }}
      </p>
      <p
        v-else-if="error"
        class="mono ko"
      >
        {{ error }}
      </p>
    </section>
  </main>
</template>

<style scoped>
.shell {
  margin: 0 auto;
  max-width: 42rem;
  padding: 4rem 1.5rem;
  font-family: system-ui, sans-serif;
  line-height: 1.6;
}

.mono {
  font-family: ui-monospace, monospace;
  font-size: 0.875rem;
}

.ok {
  color: #15803d;
}

.ko {
  color: #b91c1c;
}
</style>
