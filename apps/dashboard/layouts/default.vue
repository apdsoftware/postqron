<script setup lang="ts">
/**
 * Guscio dell'applicazione: barra superiore fissa, barra laterale, area del
 * contenuto che scorre (template Flowbite, `layouts/_default/dashboard.html`).
 *
 * ## Cosa decide questo file per tutte le schermate che verranno
 *
 * - **Le pagine ricevono un'area, non una pagina.** Sotto lo slot non c'è né
 *   contenitore né spaziatura: le mette ogni pagina, perché una tabella a tutta
 *   larghezza e una scheda di dettaglio vogliono margini diversi e un padding
 *   deciso qui andrebbe poi disfatto con classi negative.
 * - **Lo scorrimento è dell'area del contenuto**, non del documento: la barra
 *   superiore e quella laterale restano ferme, che è il comportamento del
 *   template ed è ciò che rende utilizzabile un elenco lungo di esecuzioni.
 * - **Il cassetto della navigazione è stato del guscio.** Sta qui e non dentro
 *   la barra laterale perché a governarlo sono in due — il pulsante nella barra
 *   superiore e la barra laterale stessa — e uno stato con due padroni deve
 *   stare sopra entrambi.
 *
 * Una schermata che non vuole questo guscio — l'accesso e la registrazione della
 * issue #25 — non lo disfa da dentro: dichiara un altro layout con
 * `definePageMeta({ layout: 'auth' })` e lo aggiunge in `layouts/`.
 */
const { t } = useLocale()

const navigationOpen = ref(false)

/**
 * Esc chiude il cassetto (R54, WCAG 2.2 2.1.2): un pannello che copre lo schermo
 * e si può chiudere solo toccando fuori non è raggiungibile da tastiera.
 * L'ascoltatore sta sul documento perché il fuoco, quando il cassetto si apre,
 * è ancora sul pulsante che l'ha aperto.
 */
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && navigationOpen.value) navigationOpen.value = false
}

onMounted(() => document.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))
</script>

<template>
  <div class="antialiased bg-gray-50 dark:bg-gray-900">
    <!--
      Salto al contenuto (R54, WCAG 2.2 2.4.1). È il primo elemento della pagina
      e resta invisibile finché non riceve il fuoco: chi naviga da tastiera
      altrimenti riattraversa barra superiore e navigazione a ogni cambio di
      schermata prima di arrivare a leggere.
    -->
    <a
      href="#main-content"
      class="sr-only focus:not-sr-only focus:absolute focus:z-40 focus:m-2 focus:rounded-lg focus:bg-primary-700 focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:text-white"
    >
      {{ t.shell.skipToContent }}
    </a>

    <AppNavbar
      :navigation-open="navigationOpen"
      @toggle-navigation="navigationOpen = !navigationOpen"
    />

    <div class="flex pt-16 overflow-hidden bg-gray-50 dark:bg-gray-900">
      <AppSidebar
        :open="navigationOpen"
        @close="navigationOpen = false"
      />

      <div class="relative w-full h-full overflow-y-auto bg-gray-50 lg:ml-64 dark:bg-gray-900">
        <!--
          `tabindex="-1"` non rende `<main>` raggiungibile col tabulatore: rende
          possibile *portarci* il fuoco. Senza, il salto sopra sposterebbe solo
          la finestra e il tabulatore successivo ripartirebbe dalla barra
          superiore, cioè da capo.
        -->
        <main
          id="main-content"
          tabindex="-1"
          class="min-h-[calc(100vh-4rem)] px-4 py-6 focus:outline-none"
        >
          <slot />
        </main>
      </div>
    </div>
  </div>
</template>
