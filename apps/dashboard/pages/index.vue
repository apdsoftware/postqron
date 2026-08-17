<script setup lang="ts">
/**
 * Panoramica.
 *
 * Oggi mostra una cosa sola e vera: se il servizio che esegue i cronjob sta
 * rispondendo. Le schede dei job, delle esecuzioni e del consumo arrivano con le
 * issue che le implementano (#26, #27, #28) e si aggiungono a questa griglia —
 * che è la griglia del template, `grid gap-4 xl:grid-cols-2 2xl:grid-cols-3`.
 *
 * Vale anche come esempio dell'impianto: la richiesta passa da `useApi()`, lo
 * stato da `useApiResource()`, e i tre esiti da `<AsyncState>`. Una vista nuova
 * si scrive così.
 */
const { public: config } = useRuntimeConfig()
const { t } = useLocale()
const api = useApi()

interface Health {
  status: string
  env: string
  version: string
}

const health = useApiResource(signal => api.request<Health>('/healthz', { signal }))

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
        <div class="flex items-center justify-between mb-4">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t.home.backendTitle }}
          </h2>
          <button
            type="button"
            class="inline-flex items-center p-2 text-sm font-medium text-center text-gray-500 rounded-lg hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-gray-700"
            :disabled="health.pending.value"
            data-testid="health-refresh"
            @click="health.refresh()"
          >
            {{ t.home.check }}
          </button>
        </div>

        <AsyncState
          :pending="health.pending.value"
          :error="health.error.value"
          @retry="health.refresh()"
        >
          <dl
            v-if="health.data.value"
            class="divide-y divide-gray-200 dark:divide-gray-700"
            data-testid="health"
          >
            <div class="flex items-center justify-between py-2">
              <dt class="text-sm font-normal text-gray-500 dark:text-gray-400">
                {{ t.home.statusLabel }}
              </dt>
              <dd class="inline-flex items-center text-sm font-medium text-green-700 dark:text-green-400">
                <AppIcon
                  name="check"
                  class="w-4 h-4 me-1"
                />
                {{ health.data.value.status }}
              </dd>
            </div>
            <div class="flex items-center justify-between py-2">
              <dt class="text-sm font-normal text-gray-500 dark:text-gray-400">
                {{ t.home.environmentLabel }}
              </dt>
              <dd class="font-mono text-sm text-gray-900 dark:text-white">
                {{ health.data.value.env }}
              </dd>
            </div>
            <div class="flex items-center justify-between py-2">
              <dt class="text-sm font-normal text-gray-500 dark:text-gray-400">
                {{ t.home.versionLabel }}
              </dt>
              <dd class="font-mono text-sm text-gray-900 dark:text-white">
                {{ health.data.value.version }}
              </dd>
            </div>
            <div class="flex items-center justify-between py-2">
              <dt class="text-sm font-normal text-gray-500 dark:text-gray-400">
                {{ t.home.apiBaseLabel }}
              </dt>
              <dd class="font-mono text-sm text-gray-900 break-all dark:text-white">
                {{ config.apiBaseUrl }}
              </dd>
            </div>
          </dl>
        </AsyncState>
      </div>
    </div>
  </div>
</template>
