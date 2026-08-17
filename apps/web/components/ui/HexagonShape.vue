<script setup lang="ts">
/**
 * Esagono decorativo del tema.
 *
 * Il template lo disegna con tre elementi e il trucco dei bordi trasparenti
 * (un rettangolo più due triangoli in ::before/::after). `clip-path` ottiene la
 * stessa forma con un elemento solo, e con i lati antialiasati invece che a
 * gradini.
 */
withDefaults(
  defineProps<{
    /** Larghezza dell'esagono in pixel. */
    width?: number
    /** Altezza della fascia centrale, fra le due punte. */
    body?: number
    /** Altezza di ciascuna punta, sopra e sotto la fascia. */
    cap?: number
  }>(),
  { width: 60, body: 37, cap: 15 },
)
</script>

<template>
  <div
    class="hexagon"
    :style="{
      '--hex-width': `${width}px`,
      '--hex-height': `${body + cap * 2}px`,
      '--hex-cap': `${(cap / (body + cap * 2)) * 100}%`,
    }"
  >
    <slot />
  </div>
</template>

<style scoped>
.hexagon {
  position: relative;
  width: var(--hex-width);
  height: var(--hex-height);
  background: var(--hex-fill, var(--pq-border));
  clip-path: polygon(
    50% 0,
    100% var(--hex-cap),
    100% calc(100% - var(--hex-cap)),
    50% 100%,
    0 calc(100% - var(--hex-cap)),
    0 var(--hex-cap)
  );

  /*
   * `clip-path` ritaglia anche la box-shadow, quindi l'ombra si dichiara come
   * filtro: `drop-shadow` segue la sagoma ritagliata invece del rettangolo.
   */
  filter: var(--hex-shadow, none);
  transition: var(--pq-transition);
}
</style>
