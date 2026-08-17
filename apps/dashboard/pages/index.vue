<script setup lang="ts">
// Placeholder dello scaffold: il layout Flowbite e la navigazione arrivano con
// la issue dedicata alla dashboard cliente (backlog 24).
//
// La chiamata all'health check è volutamente lato client: la dashboard è una SPA
// statica e ogni dato dinamico passa dal backend Go.
const { public: config } = useRuntimeConfig()
const { t } = useLocale()

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
  catch {
    /*
     * Il messaggio dell'eccezione arriva da `$fetch` ed è in inglese, sempre:
     * mostrarlo significherebbe una frase non tradotta in mezzo a quattro
     * lingue. Resta nella console del browser per chi sviluppa; all'utente va
     * il testo tradotto.
     */
    health.value = null
    error.value = t.value.home.unreachable
  }
  finally {
    pending.value = false
  }
}

/*
 * Titolo reattivo: `useHead` con un oggetto statico lo fisserebbe alla lingua
 * dell'avvio, e cambiando lingua la scheda del browser resterebbe indietro.
 */
useHead(computed(() => ({ title: t.value.home.title })))
</script>

<template>
  <main class="page">
    <h1>{{ t.home.title }}</h1>
    <p>{{ t.home.intro }}</p>

    <section>
      <h2>{{ t.home.backendTitle }}</h2>
      <p class="mono">
        {{ t.home.apiBaseLabel }}: {{ config.apiBaseUrl }}
      </p>
      <button
        type="button"
        :disabled="pending"
        @click="checkHealth"
      >
        {{ pending ? t.home.checking : t.home.check }}
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
.page {
  margin: 0 auto;
  max-width: 42rem;
  padding: 3rem 1.5rem;
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
