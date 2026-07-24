<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  dismissLabel?: string
  dismissible?: boolean
  title: string
  tone?: 'info' | 'success' | 'warning' | 'danger'
}>(), {
  dismissLabel: 'Chiudi messaggio',
  dismissible: false,
  tone: 'info',
})

defineEmits<{
  dismiss: []
}>()

const liveRole = computed(() => props.tone === 'danger' ? 'alert' : 'status')
</script>

<template>
  <div
    class="pq-alert"
    :class="`pq-alert--${tone}`"
    :role="liveRole"
  >
    <span
      class="pq-alert__icon"
      aria-hidden="true"
    >
      <svg
        viewBox="0 0 24 24"
        focusable="false"
      >
        <circle
          cx="12"
          cy="12"
          r="9"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
        />
        <path
          d="M12 10v6m0-9v.01"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
        />
      </svg>
    </span>
    <div class="pq-alert__content">
      <p class="pq-alert__title">
        {{ title }}
      </p>
      <div class="pq-alert__body">
        <slot />
      </div>
    </div>
    <button
      v-if="dismissible"
      class="pq-alert__dismiss"
      type="button"
      :aria-label="dismissLabel"
      @click="$emit('dismiss')"
    >
      <svg
        viewBox="0 0 24 24"
        aria-hidden="true"
        focusable="false"
      >
        <path
          d="m7 7 10 10M17 7 7 17"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
        />
      </svg>
    </button>
  </div>
</template>
