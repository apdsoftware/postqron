<script setup lang="ts">
defineProps<{
  number: string
  title: string
}>()
</script>

<template>
  <article class="feature-card">
    <span aria-hidden="true">{{ number }}</span>
    <h3>{{ title }}</h3>
    <p><slot /></p>
  </article>
</template>
