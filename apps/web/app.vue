<script setup lang="ts">
/*
 * `lang` non si imposta qui: dipende dalla pagina, ed è `useLocalizedHead()` a
 * dichiararlo insieme a `canonical` e `hreflang`. Il suffisso del titolo,
 * invece, è il nome del prodotto e vale in tutte le lingue.
 */
useHead({
  titleTemplate: title => (title ? `${title} · Postqron` : 'Postqron'),
})
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
</template>
