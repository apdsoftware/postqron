<script setup lang="ts">
withDefaults(defineProps<{
  description?: string
  eyebrow?: string
  title: string
}>(), {
  description: undefined,
  eyebrow: undefined,
})
</script>

<template>
  <header class="admin-page__heading">
    <p
      v-if="eyebrow"
      class="admin-eyebrow"
    >
      {{ eyebrow }}
    </p>
    <h1>{{ title }}</h1>
    <p v-if="description">
      {{ description }}
    </p>
    <div
      v-if="$slots.actions"
      class="admin-page__actions"
    >
      <slot name="actions" />
    </div>
  </header>
</template>
