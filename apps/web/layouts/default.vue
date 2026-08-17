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
    <SiteHeader />
    <main>
      <slot />
    </main>
    <SiteFooter />
    <ClientOnly>
      <CookieBanner />
    </ClientOnly>
  </div>
</template>
