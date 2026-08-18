<script setup lang="ts">
import type { LocaleCode } from '~/utils/locale'
import { LOCALES } from '~/utils/locale'

/**
 * Selettore di lingua (R32).
 *
 * Anche qui, come sul sito pubblico, cambiare lingua **cambia indirizzo**: la
 * lingua sta nel percorso (SPEC §8-bis) e una pagina che si legge in francese
 * mentre l'indirizzo dice `/it/…` è un indirizzo che mente a chi lo copia.
 *
 * Resta però un `<select>` e non un elenco di link, perché la differenza fra le
 * due applicazioni non è sparita, si è spostata: là ogni lingua è un insieme di
 * file diverso e il salto è un caricamento vero, qui è un guscio unico e la
 * navigazione avviene tutta nel browser. Quello che ne discende è il requisito
 * che questo componente deve rispettare, e che l'e2e verifica: **la pagina non
 * si ricarica e non perde quello che aveva** — i dati già scaricati, la
 * posizione nell'elenco, la password scritta a metà. Se ne occupa la chiave di
 * `<NuxtPage>` in `app.vue`, che è il percorso senza lingua e quindi non cambia.
 *
 * È un `<select>` nativo anche per la ragione di prima, che vale ancora: non la
 * tendina di Flowbite, che è un `<button>` più un `<div>` di voci governati da
 * `flowbite.js`. Con il `<select>` tastiera,
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
 * `v-model` con getter e setter, e non un `ref` da tenere allineato: la lingua
 * corrente si legge dall'indirizzo e si cambia con `setLocale()`, che ricorda la
 * scelta (R32) e naviga. Una copia locale dovrebbe essere risincronizzata a ogni
 * navigazione — compreso il tasto «indietro», che riporta all'indirizzo
 * precedente e quindi alla lingua precedente senza passare da qui.
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
