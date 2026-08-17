<script setup lang="ts">
withDefaults(
  defineProps<{
    /**
     * Immagine di sfondo sotto il gradiente, con effetto parallasse.
     *
     * Il tema usa parallax.js (che a sua volta richiede jQuery) per due fasce
     * decorative. Qui la stessa fascia funziona anche senza immagine — è il
     * caso della home — e quando c'è, lo scorrimento è affidato a
     * `background-attachment: fixed`: nessuna libreria, nessun listener di
     * scroll, e il compositore del browser fa il lavoro.
     */
    image?: string
    /** Altezza minima della fascia. */
    minHeight?: number
  }>(),
  { image: undefined, minHeight: 315 },
)
</script>

<template>
  <section
    class="gradient-band"
    :style="{
      '--band-min-height': `${minHeight}px`,
      '--band-image': image ? `url('${image}')` : undefined,
    }"
  >
    <div class="gradient-band__content">
      <div class="container">
        <slot />
      </div>
    </div>
  </section>
</template>

<style scoped>
.gradient-band {
  position: relative;
  min-height: var(--band-min-height);
  overflow: hidden;
  background-image: var(--band-image, none);
  background-position: center;
  background-size: cover;
  background-attachment: fixed;
}

/*
 * Il gradiente deborda del 20% per lato: ruotato di 127°, senza il margine
 * mostrerebbe gli angoli scoperti.
 */
.gradient-band::before {
  content: '';
  position: absolute;
  top: -20%;
  left: -20%;
  width: 140%;
  height: 140%;
  z-index: 2;
  opacity: 0.78;
  background-image: var(--pq-gradient-band);
}

.gradient-band__content {
  position: absolute;
  top: 50%;
  width: 100%;
  z-index: 3;
  transform: perspective(1px) translateY(-50%);
}

@media (max-width: 991px) {
  .gradient-band {
    padding-top: var(--pq-space-12);
    padding-bottom: var(--pq-space-12);

    /* Su iOS `fixed` non è supportato e produce uno sfondo che salta. */
    background-attachment: scroll;
  }

  .gradient-band__content {
    position: relative;
    top: 0;
    transform: none;
  }
}
</style>
