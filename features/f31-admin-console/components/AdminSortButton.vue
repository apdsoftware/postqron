<script setup lang="ts">
defineProps<{
  label: string
  active: boolean
  direction: 'asc' | 'desc'
}>()

defineEmits<{
  select: []
}>()
</script>

<template>
  <button
    class="admin-sort"
    type="button"
    :aria-label="label"
    @click="$emit('select')"
  >
    <span><slot /></span>
    <span
      v-if="active"
      aria-hidden="true"
    >{{ direction === 'asc' ? '↑' : '↓' }}</span>
  </button>
</template>

<style scoped>
.admin-sort {
  display: inline-flex;
  align-items: center;
  gap: var(--pq-space-1);
  border: 0;
  padding: 0;
  color: inherit;
  background: transparent;
  font: inherit;
  text-transform: inherit;
  cursor: pointer;
}

.admin-sort:focus-visible {
  outline: 3px solid var(--pq-color-focus);
  outline-offset: 3px;
}
</style>
