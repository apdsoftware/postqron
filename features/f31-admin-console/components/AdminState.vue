<script setup lang="ts">
withDefaults(defineProps<{
  message: string
  retryLabel?: string
  variant: 'empty' | 'error' | 'loading'
}>(), {
  retryLabel: undefined,
})
const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <div
    v-if="variant === 'error'"
    class="admin-state admin-state--error"
    role="alert"
  >
    <p>{{ message }}</p>
    <button
      v-if="retryLabel"
      class="pq-button pq-button--secondary"
      type="button"
      @click="emit('retry')"
    >
      {{ retryLabel }}
    </button>
  </div>
  <p
    v-else
    class="admin-state"
    role="status"
    aria-live="polite"
  >
    {{ message }}
  </p>
</template>
