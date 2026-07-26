<script setup lang="ts">
withDefaults(defineProps<{
  title?: string
  variant: 'danger' | 'info' | 'success' | 'warning'
}>(), {
  title: undefined,
})
</script>

<template>
  <div
    class="pq-alert"
    :class="`pq-alert--${variant}`"
    :role="variant === 'danger' ? 'alert' : 'status'"
    aria-live="polite"
  >
    <span
      class="pq-alert__icon"
      aria-hidden="true"
    >●</span>
    <div class="pq-alert__body">
      <p
        v-if="title"
        class="pq-alert__title"
      >
        {{ title }}
      </p>
      <p><slot /></p>
    </div>
  </div>
</template>
