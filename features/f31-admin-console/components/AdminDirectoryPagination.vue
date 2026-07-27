<script setup lang="ts">
import { computed } from '#imports'

const page = defineModel<number>('page', { required: true })
const pageSize = defineModel<number>('pageSize', { required: true })

const props = defineProps<{
  total: number
  previousLabel: string
  nextLabel: string
  pageSizeLabel: string
  statusLabel: (page: number, pageCount: number, total: number) => string
  disabled?: boolean
}>()

const pageCount = computed(() => Math.max(1, Math.ceil(props.total / pageSize.value)))

function previous() {
  if (page.value > 1 && !props.disabled) {
    page.value -= 1
  }
}

function next() {
  if (page.value < pageCount.value && !props.disabled) {
    page.value += 1
  }
}

function changePageSize() {
  page.value = 1
}
</script>

<template>
  <nav
    v-if="total > 0"
    class="admin-directory-pagination"
    :aria-label="statusLabel(page, pageCount, total)"
  >
    <label>
      <span>{{ pageSizeLabel }}</span>
      <select
        v-model.number="pageSize"
        :disabled="disabled"
        @change="changePageSize"
      >
        <option
          v-for="size in [10, 25, 50, 100]"
          :key="size"
          :value="size"
        >
          {{ size }}
        </option>
      </select>
    </label>
    <div>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :disabled="disabled || page <= 1"
        @click="previous"
      >
        {{ previousLabel }}
      </button>
      <p aria-live="polite">
        {{ statusLabel(page, pageCount, total) }}
      </p>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :disabled="disabled || page >= pageCount"
        @click="next"
      >
        {{ nextLabel }}
      </button>
    </div>
  </nav>
</template>

<style scoped>
.admin-directory-pagination {
  align-items: center;
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  justify-content: space-between;
}

.admin-directory-pagination label,
.admin-directory-pagination div {
  align-items: center;
  display: flex;
  gap: .65rem;
}

.admin-directory-pagination select {
  min-height: 2.75rem;
  min-width: 4.5rem;
}

.admin-directory-pagination p {
  margin: 0;
  min-width: 10rem;
  text-align: center;
}

@media (max-width: 540px) {
  .admin-directory-pagination,
  .admin-directory-pagination div {
    align-items: stretch;
    width: 100%;
  }

  .admin-directory-pagination div {
    display: grid;
    grid-template-columns: 1fr;
  }

  .admin-directory-pagination p {
    order: -1;
  }
}
</style>
