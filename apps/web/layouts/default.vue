<script setup lang="ts">
/*
 * Senza JavaScript nulla può togliere il velo di caricamento né far comparire i
 * blocchi animati: lo stile del <noscript> annulla entrambi gli stati iniziali.
 * Va nell'head, non nel corpo, perché deve valere prima del primo disegno.
 */
useHead({
  noscript: [
    {
      innerHTML:
        '<style>.preloader{display:none}.pq-reveal{opacity:1;transform:none}</style>',
      tagPosition: 'head',
    },
  ],
})
</script>

<template>
  <div>
    <SitePreloader />
    <!--
      Il banner apre il documento pur restando in basso sullo schermo: è
      `position: fixed`, quindi l'ordine nel markup non ne cambia la resa ma
      decide chi lo incontra per primo. Chi naviga da tastiera o con uno
      screen reader trova la scelta sui cookie al primo Tab invece che dopo
      l'intera pagina e il piè di pagina — senza che il banner debba rubare il
      fuoco a chi sta già leggendo.
    -->
    <ClientOnly>
      <CookieBanner />
    </ClientOnly>
    <SiteHeader />
    <main>
      <slot />
    </main>
    <SiteFooter />
  </div>
</template>
