<script setup lang="ts">
import { computed, useId } from 'vue'

const props = withDefaults(defineProps<{
  headingLevel?: 2 | 3 | 4 | 5 | 6
  title?: string
}>(), {
  headingLevel: 2,
  title: undefined,
})

const titleId = `pq-card-${useId()}`
const headingTag = computed(() => `h${props.headingLevel}`)
</script>

<template>
  <article
    class="pq-card"
    :aria-labelledby="title && !$slots.header ? titleId : undefined"
  >
    <header
      v-if="title || $slots.header"
      class="pq-card__header"
    >
      <slot name="header">
        <component
          :is="headingTag"
          :id="titleId"
          class="pq-card__title"
        >
          {{ title }}
        </component>
      </slot>
    </header>
    <div class="pq-card__body">
      <slot />
    </div>
    <footer
      v-if="$slots.footer"
      class="pq-card__footer"
    >
      <slot name="footer" />
    </footer>
  </article>
</template>
