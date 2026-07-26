<script setup lang="ts">
defineProps<{
  eyebrow: string
  title: string
  description?: string
}>()
</script>

<template>
  <header class="admin-page__heading">
    <p class="admin-eyebrow">
      {{ eyebrow }}
    </p>
    <h1>{{ title }}</h1>
    <p v-if="description">
      {{ description }}
    </p>
  </header>
</template>
