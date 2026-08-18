<script setup lang="ts">
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
   * ricerca. È anche il motivo per cui, a differenza del sito pubblico, qui non
   * esistono `hreflang` né `canonical`: dichiarare le lingue alternative di
   * pagine che nessun crawler deve indicizzare sarebbe lavoro inutile.
   */
  meta: [{ name: 'robots', content: 'noindex, nofollow' }],
})
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
</template>
