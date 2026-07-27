<script setup lang="ts">
defineProps<{
  label: string
  csvLabel: string
  xlsxLabel: string
  limitLabel: string
  csvHref: string
  xlsxHref: string
}>()
</script>

<template>
  <aside
    class="admin-export"
    :aria-label="label"
  >
    <p>{{ limitLabel }}</p>
    <div>
      <a
        class="pq-button pq-button--secondary"
        :href="csvHref"
        download
      >
        {{ csvLabel }}
      </a>
      <a
        class="pq-button pq-button--secondary"
        :href="xlsxHref"
        download
      >
        {{ xlsxLabel }}
      </a>
    </div>
  </aside>
</template>

<style scoped>
.admin-export {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--pq-space-3);
}

.admin-export p {
  margin: 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
}

.admin-export div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pq-space-2);
}
</style>
