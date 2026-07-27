<script setup lang="ts">
import { computed } from '#imports'

const page = defineModel<number>('page', { required: true })

const props = defineProps<{
  total: number
  pageSize: number
  previousLabel: string
  nextLabel: string
  statusLabel: (page: number, pageCount: number) => string
}>()

const pageCount = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))

function previous() {
  if (page.value > 1) {
    page.value -= 1
  }
}

function next() {
  if (page.value < pageCount.value) {
    page.value += 1
  }
}
</script>

<template>
  <nav
    v-if="total > 0"
    class="admin-pagination"
    :aria-label="statusLabel(page, pageCount)"
  >
    <button
      class="pq-button pq-button--secondary"
      type="button"
      :disabled="page <= 1"
      @click="previous"
    >
      {{ previousLabel }}
    </button>
    <p aria-live="polite">
      {{ statusLabel(page, pageCount) }}
    </p>
    <button
      class="pq-button pq-button--secondary"
      type="button"
      :disabled="page >= pageCount"
      @click="next"
    >
      {{ nextLabel }}
    </button>
  </nav>
</template>
