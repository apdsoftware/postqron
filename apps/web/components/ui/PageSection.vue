<script setup lang="ts">
withDefaults(
  defineProps<{
    /** Fondo bianco invece dell'azzurro di pagina. */
    white?: boolean
    /** Spaziatura ridotta: 60px sopra e sotto invece di 100/70. */
    tight?: boolean
    /** Filigrana esagonale agganciata a un lato del riquadro. */
    watermark?: 'left' | 'right'
    /** Linea di chiusura in fondo alla sezione. */
    divider?: boolean
  }>(),
  { watermark: undefined },
)
</script>

<template>
  <section
    class="section"
    :class="{
      'section--white': white,
      'section--tight': tight,
      'section--divider': divider,
      [`section--watermark-${watermark}`]: watermark,
    }"
  >
    <div class="container">
      <slot />
    </div>
  </section>
</template>

<style scoped>
.section {
  padding-top: 100px;
  padding-bottom: 70px;
}

.section--white {
  background-color: var(--pq-surface);
}

.section--tight {
  padding-top: 60px;
  padding-bottom: 60px;
}

.section--divider {
  border-bottom: 1px solid var(--pq-border-hairline);
}

.section--watermark-right {
  background-image: url('~/assets/images/watermark-right.svg');
  background-position: center right;
  background-repeat: no-repeat;
}

.section--watermark-left {
  background-image: url('~/assets/images/watermark-left.svg');
  background-position: center left;
  background-repeat: no-repeat;
}
</style>
