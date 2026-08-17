<script setup lang="ts">
withDefaults(
  defineProps<{
    /** Destinazione: interna (`/prezzi`) o assoluta. `NuxtLink` distingue da sé. */
    to: string
    /** `solid` inverte i colori: sfondo pieno, testo bianco. */
    variant?: 'outline' | 'solid'
  }>(),
  { variant: 'outline' },
)
</script>

<template>
  <NuxtLink
    :to="to"
    class="line-button"
    :class="`line-button--${variant}`"
  >
    <slot />
  </NuxtLink>
</template>

<style scoped>
.line-button {
  display: inline-block;
  width: 160px;
  height: 36px;
  border: 1px solid var(--pq-primary);
  border-radius: var(--pq-radius-pill);
  font-size: var(--pq-text-2xs);
  font-weight: var(--pq-weight-bold);
  letter-spacing: 0.75px;
  line-height: 34px;
  text-align: center;
  text-transform: uppercase;
  transition: var(--pq-transition);
}

.line-button--outline {
  background: var(--pq-surface);
  color: var(--pq-primary);
}

.line-button--outline:hover {
  background: var(--pq-primary);
  color: var(--pq-text-inverted);
}

.line-button--solid {
  background: var(--pq-primary);
  color: var(--pq-text-inverted);
}

.line-button--solid:hover {
  background: var(--pq-surface);
  color: var(--pq-primary);
}
</style>
