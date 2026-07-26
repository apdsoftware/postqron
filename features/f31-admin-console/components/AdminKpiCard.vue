<script setup lang="ts">
withDefaults(defineProps<{
  hint?: string
  label: string
  status?: 'degraded' | 'healthy' | 'neutral'
  value: string
}>(), {
  hint: undefined,
  status: 'neutral',
})
</script>

<template>
  <article
    class="pq-card admin-kpi-card"
    :data-status="status"
  >
    <div class="pq-card__body">
      <p class="admin-kpi-card__label">
        {{ label }}
      </p>
      <p class="admin-kpi-card__value">
        {{ value }}
      </p>
      <p
        v-if="hint"
        class="admin-kpi-card__hint"
      >
        {{ hint }}
      </p>
    </div>
  </article>
</template>
