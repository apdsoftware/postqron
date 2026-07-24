<script setup lang="ts">
withDefaults(defineProps<{
  disabled?: boolean
  fullWidth?: boolean
  loading?: boolean
  loadingLabel?: string
  responsive?: boolean
  type?: 'button' | 'submit' | 'reset'
  variant?: 'primary' | 'secondary' | 'quiet' | 'danger'
}>(), {
  disabled: false,
  fullWidth: false,
  loading: false,
  loadingLabel: 'Attendi',
  responsive: false,
  type: 'button',
  variant: 'primary',
})
</script>

<template>
  <button
    class="pq-button"
    :class="`pq-button--${variant}`"
    :type="type"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
    :data-full-width="fullWidth || undefined"
    :data-responsive="responsive || undefined"
  >
    <span
      v-if="loading"
      class="pq-button__spinner"
      aria-hidden="true"
    />
    <span v-if="loading">{{ loadingLabel }}</span>
    <slot v-else />
  </button>
</template>
