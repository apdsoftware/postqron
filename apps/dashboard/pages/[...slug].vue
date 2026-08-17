<script setup lang="ts">
/**
 * Indirizzo che non corrisponde a nessuna schermata.
 *
 * ## Perché una pagina e non `error.vue`
 *
 * `error.vue` di Nuxt riceve gli errori che il rendering solleva, e sostituisce
 * l'applicazione intera: chi ci finisce perde il guscio — barra superiore,
 * navigazione, selettore di lingua — proprio nel momento in cui gli servirebbe
 * un modo di andare altrove. Un indirizzo sbagliato non è un guasto
 * dell'applicazione: è una schermata, e come tale sta in `pages/`.
 *
 * ## Perché serve davvero
 *
 * La dashboard è una SPA statica: `public/_redirects` fa servire `index.html`
 * per qualunque percorso, quindi il server non dà mai 404 (ed è voluto — un
 * aggiornamento di pagina su `/jobs/42` deve funzionare). La conseguenza è che
 * *l'unico* posto in cui si può dire «questo indirizzo non esiste» è il router
 * lato client. Senza questa pagina, un refuso nell'indirizzo darebbe il guscio
 * con l'area del contenuto vuota — che si legge come un guasto, non come un
 * indirizzo sbagliato.
 */
const { t } = useLocale()

useHead(computed(() => ({ title: t.value.notFound.title })))
</script>

<template>
  <div class="flex flex-col items-center justify-center px-6 py-16 text-center">
    <h1 class="mb-3 text-2xl font-bold leading-tight text-gray-900 sm:text-4xl dark:text-white">
      {{ t.notFound.title }}
    </h1>
    <p class="mb-5 max-w-lg text-base font-normal text-gray-500 md:text-lg dark:text-gray-400">
      {{ t.notFound.intro }}
    </p>
    <NuxtLink
      to="/"
      class="text-white bg-primary-700 hover:bg-primary-800 focus:ring-4 focus:ring-primary-300 font-medium rounded-lg text-sm px-5 py-2.5 text-center inline-flex items-center dark:bg-primary-600 dark:hover:bg-primary-700 dark:focus:ring-primary-800"
    >
      <AppIcon
        name="back"
        class="w-5 h-5 mr-2 -ml-1"
      />
      {{ t.notFound.back }}
    </NuxtLink>
  </div>
</template>
