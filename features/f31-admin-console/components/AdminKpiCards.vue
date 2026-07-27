<script setup lang="ts">
export interface AdminKpi {
  key: string
  label: string
  value: string
  tone?: 'default' | 'success' | 'warning'
}

defineProps<{
  items: readonly AdminKpi[]
}>()
</script>

<template>
  <ul class="admin-kpis">
    <li
      v-for="item in items"
      :key="item.key"
      class="admin-kpi"
      :data-tone="item.tone ?? 'default'"
    >
      <span class="admin-kpi__value">{{ item.value }}</span>
      <span class="admin-kpi__label">{{ item.label }}</span>
    </li>
  </ul>
</template>
