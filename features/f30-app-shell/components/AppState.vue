<script setup lang="ts">
import { computed } from '#imports'
import { useAppShellI18n } from './core/use-app-shell.ts'

type AppStateKind = 'access-denied' | 'empty' | 'loading' | 'offline'

const props = withDefaults(defineProps<{
  action?: boolean
  kind: AppStateKind
}>(), {
  action: false,
})
const emit = defineEmits<{
  retry: []
}>()
const { t } = useAppShellI18n()
const messageKey = computed<'denied' | 'empty' | 'loading' | 'offline'>(() =>
  props.kind === 'access-denied'
  ? 'denied'
  : props.kind)
const icon = computed(() => {
  switch (props.kind) {
    case 'access-denied':
      return '!'
    case 'empty':
      return '○'
    case 'offline':
      return '↻'
    default:
      return '…'
  }
})
</script>

<template>
  <section
    class="app-state"
    :class="`app-state--${props.kind}`"
    :aria-busy="props.kind === 'loading' ? 'true' : undefined"
    :role="props.kind === 'access-denied' ? 'alert' : 'status'"
    aria-live="polite"
  >
    <span
      class="app-state__icon"
      aria-hidden="true"
    >{{ icon }}</span>
    <div>
      <h1>{{ t(`state.${messageKey}.title`) }}</h1>
      <p>{{ t(`state.${messageKey}.description`) }}</p>
      <button
        v-if="props.action"
        class="pq-button pq-button--secondary"
        type="button"
        @click="emit('retry')"
      >
        {{ t('state.retry') }}
      </button>
    </div>
  </section>
</template>
