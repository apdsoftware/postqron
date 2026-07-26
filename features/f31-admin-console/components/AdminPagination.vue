<script setup lang="ts">
defineProps<{
  nextLabel: string
  page: number
  pageCount: number
  previousLabel: string
  status: string
}>()
const emit = defineEmits<{ change: [page: number] }>()
</script>

<template>
  <nav
    class="admin-pagination"
    :aria-label="status"
  >
    <button
      class="pq-button pq-button--secondary"
      type="button"
      :disabled="page <= 1"
      @click="emit('change', page - 1)"
    >
      {{ previousLabel }}
    </button>
    <p role="status">
      {{ status }}
    </p>
    <button
      class="pq-button pq-button--secondary"
      type="button"
      :disabled="page >= pageCount"
      @click="emit('change', page + 1)"
    >
      {{ nextLabel }}
    </button>
  </nav>
</template>
