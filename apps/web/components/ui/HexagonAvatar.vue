<script setup lang="ts">
/**
 * Ritratto ritagliato a esagono, con la cornice bianca del tema.
 *
 * Nel template l'esagono è ottenuto con tre div annidati e tre rotazioni di
 * 60°, più un PNG per la cornice: sette elementi e un'immagine per una
 * maschera. `clip-path` fa la stessa cosa con due, e l'immagine sotto resta una
 * vera `<img>` — quindi con testo alternativo e caricamento pigro.
 */
withDefaults(
  defineProps<{
    src: string
    alt: string
    /** Larghezza dell'esagono in pixel; l'altezza segue le proporzioni del tema. */
    size?: number
  }>(),
  { size: 93 },
)
</script>

<template>
  <div
    class="hexagon-avatar"
    :style="{ '--avatar-width': `${size}px` }"
  >
    <img
      :src="src"
      :alt="alt"
      class="hexagon-avatar__image"
      loading="lazy"
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
  background: #fff;
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
