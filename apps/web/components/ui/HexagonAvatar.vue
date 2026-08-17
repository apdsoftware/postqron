<script setup lang="ts">
/**
 * Ritratto ritagliato a esagono, con la cornice bianca del tema.
 *
 * Nel template l'esagono è ottenuto con tre div annidati e tre rotazioni di
 * 60°, più un PNG per la cornice: sette elementi e un'immagine per una
 * maschera. `clip-path` fa la stessa cosa con due, e l'immagine sotto resta una
 * vera `<img>` — quindi con testo alternativo e caricamento pigro.
 */
const props = withDefaults(
  defineProps<{
    src: string
    alt: string
    /** Larghezza dell'esagono in pixel; l'altezza segue le proporzioni del tema. */
    size?: number
  }>(),
  { size: 93 },
)

/*
 * Le stesse due proporzioni del CSS qui sotto, in numeri interi, per gli
 * attributi `width` e `height`. Non sono ridondanti: il CSS con ambito arriva
 * dopo il markup, e nell'intervallo il browser deve già sapere quanto spazio
 * riservare — senza, ogni ritratto che si carica sposta la citazione accanto.
 */
const width = computed(() => Math.round(props.size * 77 / 93))
const height = computed(() => Math.round(props.size * 88 / 93))
</script>

<template>
  <div
    class="hexagon-avatar"
    :style="{ '--avatar-width': `${size}px` }"
  >
    <img
      :src="src"
      :alt="alt"
      :width="width"
      :height="height"
      class="hexagon-avatar__image"
      loading="lazy"
      decoding="async"
    >
  </div>
</template>

<style scoped>
.hexagon-avatar {
  display: grid;
  place-items: center;
  width: var(--avatar-width);

  /* 106/93: le proporzioni della cornice esagonale del tema. */
  height: calc(var(--avatar-width) * 106 / 93);

  /* La cornice bianca è l'esagono esterno, la foto quello interno. */
  background: var(--pq-surface);
  clip-path: polygon(50% 0, 100% 25%, 100% 75%, 50% 100%, 0 75%, 0 25%);
  transition: var(--pq-transition);
}

.hexagon-avatar__image {
  display: block;
  width: calc(var(--avatar-width) * 77 / 93);
  height: calc(var(--avatar-width) * 88 / 93);
  object-fit: cover;
  clip-path: polygon(50% 0, 100% 25%, 100% 75%, 50% 100%, 0 75%, 0 25%);
}
</style>
