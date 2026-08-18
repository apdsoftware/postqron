<script setup lang="ts">
import { stripLocale } from '~/utils/locale'

const { htmlLang } = useLocale()

/*
 * Chiamato qui e non nel guscio: la classe `dark` sta sull'elemento radice, che
 * è fuori dal layout, e chiamarlo alla radice dell'applicazione garantisce che
 * lo stato esista prima di qualunque componente che lo legga. Lo script in
 * `nuxt.config.ts` ha già messo la classe prima del primo pixel; da qui in poi
 * la governa questo.
 */
useColorScheme()

useHead({
  titleTemplate: title => (title ? `${title} · Postqron` : 'Postqron'),

  /*
   * `lang` segue la lingua scelta (R31, R32) invece di essere fisso.
   * Non è un dettaglio formale: è ciò che dice ai lettori di schermo con quale
   * fonetica leggere la pagina, e al browser quali regole di sillabazione usare.
   */
  htmlAttrs: { lang: htmlLang },

  /*
   * La dashboard sta dietro autenticazione e non ha valore per i motori di
   * ricerca. **Prefissata per lingua non vuol dire indicizzabile**: le rotte
   * hanno il prefisso di §8-bis perché un indirizzo dev'essere condivisibile e
   * componibile da fuori, non perché qualcuno debba trovarle. Per questo qui
   * non esistono `hreflang` né `canonical` — dichiarare le lingue alternative
   * di pagine che nessun crawler deve indicizzare sarebbe lavoro inutile — e in
   * `public/robots.txt` non esiste un `Disallow`, che impedirebbe di leggere
   * proprio questa riga.
   */
  meta: [{ name: 'robots', content: 'noindex, nofollow' }],
})
</script>

<template>
  <NuxtLayout>
    <!--
      La chiave della pagina è il percorso **senza lingua**.

      È ciò che rende il cambio di lingua un cambio di lingua e non una
      ripartenza. La chiave predefinita di Nuxt è il percorso con dentro i
      parametri: da `/it/jobs/42` a `/fr/jobs/42` cambierebbe, il componente si
      smonterebbe e si rimonterebbe, e con lui se ne andrebbero i dati già
      caricati, la posizione nell'elenco e ciò che si stava scrivendo in un
      modulo — la password a metà, sulla schermata di accesso. Con questa chiave
      cambiano solo i testi, che è tutto quello che era stato chiesto.

      `/it/jobs/42` → `/it/jobs/43` resta invece un cambio di schermata, perché
      lì la chiave cambia davvero.
    -->
    <NuxtPage :page-key="route => stripLocale(route.path)" />
  </NuxtLayout>
</template>
