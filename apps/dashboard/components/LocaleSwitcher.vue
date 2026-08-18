<script setup lang="ts">
import type { LocaleCode } from '~/utils/locale'
import { LOCALES } from '~/utils/locale'

/**
 * Selettore di lingua (R32).
 *
 * Sul sito pubblico il selettore è un elenco di link: cambiare lingua significa
 * cambiare indirizzo, perché ogni lingua è un insieme di pagine pre-renderizzate
 * diverso. Qui non c'è nulla da navigare — la dashboard è una SPA e i testi sono
 * già tutti nel bundle — quindi il controllo giusto è un campo di scelta, non
 * una navigazione: la pagina resta dov'è e cambia lingua sul posto.
 *
 * È un `<select>` nativo e non la tendina di Flowbite, che è un `<button>` più
 * un `<div>` di voci governati da `flowbite.js`. Con il `<select>` tastiera,
 * lettori di schermo e ruota di scelta del telefono funzionano senza che si
 * debba riscrivere ciò che il browser già fa — e senza i 29 KB di JavaScript
 * che quella tendina porterebbe con sé. La veste è quella dei campi del
 * template (`form-select` di Flowbite), la semantica resta del browser.
 *
 * Sta nella barra superiore e non nel piede della barra laterale, dove lo mette
 * il template: quel piede è `hidden lg:flex` e sul telefono sparirebbe, mentre
 * R32 vuole il selettore nell'interfaccia — cioè su ogni schermata e a ogni
 * larghezza.
 */
const { locale, t, setLocale } = useLocale()

/**
 * `v-model` con getter e setter invece che sullo stato diretto: la scelta deve
 * passare da `setLocale()`, che è ciò che la rende persistente. Scrivere sullo
 * stato la applicherebbe soltanto a questa visita.
 */
const selected = computed<LocaleCode>({
  get: () => locale.value,
  set: code => setLocale(code),
})
</script>

<template>
  <select
    v-model="selected"
    class="block p-2 pe-9 text-sm text-gray-900 border border-gray-300 rounded-lg bg-gray-50 focus:ring-primary-500 focus:border-primary-500 dark:bg-gray-700 dark:border-gray-600 dark:text-white dark:focus:ring-primary-500 dark:focus:border-primary-500"
    :aria-label="t.shell.languageLabel"
    data-testid="locale-switcher"
  >
    <!--
      Il nome di ogni lingua è scritto nella lingua stessa, e `lang` lo dichiara:
      senza, la sintesi vocale legge «Deutsch» con la fonetica della lingua
      corrente. Non è contenuto tradotto, quindi non sta in `content/`.
    -->
    <option
      v-for="entry in LOCALES"
      :key="entry.code"
      :value="entry.code"
      :lang="entry.htmlLang"
    >
      {{ entry.label }}
    </option>
  </select>
</template>
