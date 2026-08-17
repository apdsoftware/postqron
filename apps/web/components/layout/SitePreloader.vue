<script setup lang="ts">
/**
 * Velo di caricamento con l'esagono che si compone a spicchi.
 *
 * Nel template resta finché non è terminato il `load` della finestra, cioè
 * finché non sono arrivati anche jQuery e i sei plugin. Qui la pagina è già
 * pre-renderizzata: il velo serve solo a coprire il primo instante di stile e
 * font, e sparisce non appena Vue è montato.
 */
const isVisible = ref(true)

onMounted(() => {
  isVisible.value = false
})
</script>

<template>
  <div
    class="preloader"
    :class="{ 'is-hidden': !isVisible }"
    aria-hidden="true"
  >
    <span
      v-for="slice in 6"
      :key="slice"
      class="preloader__slice"
    />
  </div>
</template>

<style scoped>
.preloader {
  position: fixed;
  width: 100%;
  height: 100%;
  z-index: 9999;
  background: var(--pq-page);
  transition: opacity 0.6s ease;
}

.preloader.is-hidden {
  opacity: 0;
  visibility: hidden;
  pointer-events: none;
}

/*
 * Ogni spicchio è un triangolo ottenuto con i bordi: sei di questi, ruotati di
 * 60° l'uno dall'altro, compongono l'esagono.
 */
.preloader__slice {
  position: absolute;
  top: 50%;
  left: 50%;
  width: 30px;
  border: 30px solid transparent;
  border-right-width: 17px;
  border-left-width: 17px;

  /*
   * Il colore dello spicchio è una tappa fra le due fermate del gradiente di
   * marca: il tema elencava sei esadecimali che non erano riconducibili a
   * nulla, e che una revisione della palette avrebbe lasciato indietro.
   */
  border-top-color: color-mix(in oklab, var(--pq-primary), var(--pq-accent-end) var(--quota));

  /* Stato di partenza: invisibile finché il ritardo dello spicchio non scade. */
  opacity: 0;
  translate: -50% -50%;
  scale: 0;
  animation: preloader-slice 2s infinite;
}

.preloader__slice:nth-child(1) {
  --quota: 0%;
  rotate: 0deg;
  animation-delay: 0.07s;
}

.preloader__slice:nth-child(2) {
  --quota: 20%;
  rotate: 60deg;
  animation-delay: 0.14s;
}

.preloader__slice:nth-child(3) {
  --quota: 40%;
  rotate: 120deg;
  animation-delay: 0.21s;
}

.preloader__slice:nth-child(4) {
  --quota: 60%;
  rotate: 180deg;
  animation-delay: 0.28s;
}

.preloader__slice:nth-child(5) {
  --quota: 80%;
  rotate: 240deg;
  animation-delay: 0.35s;
}

.preloader__slice:nth-child(6) {
  --quota: 100%;
  rotate: 300deg;
  animation-delay: 0.42s;
}

/*
 * Il tema ripete sei fotogrammi identici a meno della rotazione, perché
 * `transform` li combinava in un'unica proprietà. Con `rotate` e `translate`
 * separati basta un'animazione sola.
 */
@keyframes preloader-slice {
  0%,
  100% {
    opacity: 0;
    scale: 0;
  }

  25%,
  75% {
    opacity: 1;
    scale: 1;
  }
}
</style>
