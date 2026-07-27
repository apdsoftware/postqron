<script setup lang="ts">
import type { ExportFormat } from '../core/contracts.ts'

defineProps<{
  csvLabel: string
  xlsxLabel: string
  limitLabel: string
  disabled?: boolean
}>()

const emit = defineEmits<{
  export: [format: ExportFormat]
}>()
</script>

<template>
  <div class="admin-directory-export">
    <p>{{ limitLabel }}</p>
    <div>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :disabled="disabled"
        @click="emit('export', 'csv')"
      >
        {{ csvLabel }}
      </button>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :disabled="disabled"
        @click="emit('export', 'xlsx')"
      >
        {{ xlsxLabel }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.admin-directory-export {
  align-items: center;
  display: flex;
  flex-wrap: wrap;
  gap: .75rem 1rem;
  justify-content: flex-end;
}

.admin-directory-export p {
  color: var(--pq-color-text-muted, #596579);
  font-size: .875rem;
  margin: 0;
}

.admin-directory-export div {
  display: flex;
  flex-wrap: wrap;
  gap: .5rem;
}
</style>
