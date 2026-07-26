<script setup lang="ts">
import { computed } from '#imports'

const props = withDefaults(defineProps<{
  variant?: 'error' | 'success' | 'info'
}>(), {
  variant: 'info',
})

const role = computed(() => (props.variant === 'error' ? 'alert' : 'status'))
</script>

<template>
  <p
    class="admin-alert"
    :class="`admin-alert--${variant}`"
    :role="role"
    aria-live="polite"
  >
    <slot />
  </p>
</template>
