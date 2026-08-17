<script setup lang="ts">
useHead({
  titleTemplate: title => (title ? `${title} · PostQron` : 'PostQron'),
  htmlAttrs: { lang: 'it' },
})
</script>

<template>
  <NuxtPage />
</template>
