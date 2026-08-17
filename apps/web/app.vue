<script setup lang="ts">
/*
 * Il sottoinsieme `latin` di Quicksand si precarica.
 *
 * Senza, il browser lo scopre solo dopo aver analizzato il CSS, e nel frattempo
 * `font-display: swap` disegna il testo con il carattere di sistema: quando
 * Quicksand arriva, ogni parola si sposta di qualche pixel dentro la propria
 * riga. Era il CLS di 0,039 della home, che Lighthouse attribuiva al «Web font
 * loaded» — e non si chiude accordando le metriche del ripiego, perché le
 * scatole non cambiano dimensione: sono i glifi a cadere in un altro punto, e
 * `size-adjust` non li sposta (le misure stanno nella PR di #513).
 *
 * Solo il `latin`: il `latin-ext` ha un `unicode-range` che nessuna delle cinque
 * lingue richiede, e precaricarlo scaricherebbe 28 KB che nessuno usa.
 *
 * L'URL arriva da `?url` e non è scritto a mano: il nome del file porta l'impronta
 * del contenuto, e un precarico che indicasse quello vecchio scaricherebbe due
 * caratteri invece di uno.
 */
import quicksandLatin from '~/assets/fonts/quicksand-latin.woff2?url'

useHead({
  /*
   * `lang` non si imposta qui: dipende dalla pagina, ed è `useLocalizedHead()` a
   * dichiararlo insieme a `canonical` e `hreflang`. Il suffisso del titolo,
   * invece, è il nome del prodotto e vale in tutte le lingue.
   */
  titleTemplate: title => (title ? `${title} · Postqron` : 'Postqron'),

  link: [{ rel: 'preload', as: 'font', type: 'font/woff2', href: quicksandLatin, crossorigin: 'anonymous' }],
})
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
</template>
