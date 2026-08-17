<script setup lang="ts">
/**
 * Panoramica.
 *
 * Le schede dei job, delle esecuzioni e del consumo arrivano con le issue che le
 * implementano (#26, #27, #28) e si aggiungono a questa griglia — che è quella
 * del template, `grid gap-4 xl:grid-cols-2 2xl:grid-cols-3`.
 *
 * La chiamata all'health check è volutamente lato client: la dashboard è una SPA
 * statica e ogni dato dinamico passa dal backend Go.
 */
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
  <div>
    <div class="mb-6">
      <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
        {{ t.home.title }}
      </h1>
      <p class="mt-1 text-base font-light text-gray-500 dark:text-gray-400">
        {{ t.home.intro }}
      </p>
    </div>

    <div class="grid gap-4 xl:grid-cols-2 2xl:grid-cols-3">
      <div class="p-4 bg-white border border-gray-200 rounded-lg shadow-sm dark:border-gray-700 sm:p-6 dark:bg-gray-800">
        <h2 class="mb-4 text-lg font-semibold text-gray-900 dark:text-white">
          {{ t.home.backendTitle }}
        </h2>

        <p class="font-mono text-sm text-gray-500 break-all dark:text-gray-400">
          {{ t.home.apiBaseLabel }}: {{ config.apiBaseUrl }}
        </p>

        <button
          type="button"
          class="px-3 py-2 mt-4 text-sm font-medium text-white rounded-lg bg-primary-700 hover:bg-primary-800 focus:ring-4 focus:ring-primary-300 disabled:opacity-60 dark:bg-primary-600 dark:hover:bg-primary-700 dark:focus:ring-primary-800"
          :disabled="pending"
          @click="checkHealth"
        >
          {{ pending ? t.home.checking : t.home.check }}
        </button>

        <p
          v-if="health"
          class="mt-4 font-mono text-sm text-green-700 dark:text-green-400"
        >
          {{ health.status }} · {{ health.env }} · {{ health.version }}
        </p>
        <p
          v-else-if="error"
          class="mt-4 font-mono text-sm text-red-700 dark:text-red-400"
        >
          {{ error }}
        </p>
      </div>
    </div>
  </div>
</template>
