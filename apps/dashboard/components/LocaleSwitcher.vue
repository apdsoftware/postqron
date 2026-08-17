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
 * È un `<select>` nativo e non una tendina costruita a mano perché così tastiera,
 * lettori di schermo e controlli di sistema funzionano senza che si debba
 * riscrivere ciò che il browser già fa. Il template Flowbite (backlog 24) potrà
 * vestirlo; la semantica non deve cambiare.
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
    class="locale-switcher"
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

<style scoped>
.locale-switcher {
  border: 1px solid #d4d4d8;
  border-radius: 0.375rem;
  background: #fff;
  padding: 0.375rem 0.5rem;
  font: inherit;
  font-size: 0.875rem;
  color: inherit;
}

.locale-switcher:focus-visible {
  outline: 2px solid #2563eb;
  outline-offset: 1px;
}
</style>
