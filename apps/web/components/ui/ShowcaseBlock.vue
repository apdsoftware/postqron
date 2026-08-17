<script setup lang="ts">
import type { RevealOptions } from '~/utils/reveal'

const props = withDefaults(
  defineProps<{
    title: string
    text: string
    /** Punti elenco, ciascuno preceduto dalla freccia del tema. */
    bullets?: readonly string[]
    /** Immagine di prodotto: sorgente, testo alternativo e dimensioni native. */
    image: string
    imageAlt: string
    imageWidth: number
    imageHeight: number
    /** Lato su cui compare l'immagine. */
    imageSide?: 'left' | 'right'
  }>(),
  { bullets: () => [], imageSide: 'left' },
)

/*
 * L'immagine entra dal lato su cui si trova, come nel tema: 30px di corsa e un
 * ritardo che la stacca dal testo già a posto.
 */
const imageReveal = computed<RevealOptions>(() => ({
  direction: props.imageSide === 'left' ? 'right' : 'left',
  distance: '30px',
  duration: 0.6,
  delay: props.imageSide === 'left' ? 0.3 : 0.4,
}))
</script>

<template>
  <div class="row">
    <div
      v-reveal="imageReveal"
      class="col-lg-6 col-md-6 col-sm-12 align-self-center showcase__media"
      :class="`showcase__media--${imageSide}`"
    >
      <img
        :src="image"
        :alt="imageAlt"
        :width="imageWidth"
        :height="imageHeight"
        class="showcase__image"
        loading="lazy"
      >
    </div>
    <div
      class="col-lg-6 col-md-6 col-sm-12 align-self-center showcase__body"
      :class="`showcase__body--${imageSide}`"
    >
      <h2 class="showcase__title">
        {{ title }}
      </h2>
      <div class="showcase__text">
        <p>{{ text }}</p>
        <ul v-if="bullets.length">
          <li
            v-for="bullet in bullets"
            :key="bullet"
          >
            <span class="showcase__bullet-icon"><HexIcon name="arrowRight" /></span>
            <span>{{ bullet }}</span>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>

<style scoped>
/*
 * Con l'immagine a destra l'ordine visivo si inverte, quello del DOM no: il
 * testo resta primo per chi legge con uno screen reader o naviga da tastiera.
 * Le posizioni sono esplicite per entrambe le colonne, così l'auto-placement
 * non ha nulla da indovinare.
 */
@media (min-width: 768px) {
  .showcase__media--right {
    grid-column-start: 7;
    grid-row-start: 1;
  }

  .showcase__body--right {
    grid-column-start: 1;
    grid-row-start: 1;
  }
}

@media (max-width: 991px) {
  .showcase__media {
    margin-bottom: var(--pq-space-6);
  }
}

.showcase__image {
  display: block;
  max-width: 100%;
  height: auto;
}

.showcase__title {
  margin-bottom: var(--pq-space-4);
  color: var(--pq-heading);
  font-size: var(--pq-text-2xl);
  font-weight: var(--pq-weight-regular);
  letter-spacing: 1.3px;
  line-height: 40px;
}

.showcase__text {
  color: var(--pq-text);
  font-size: var(--pq-text-base);
  letter-spacing: 1px;
  line-height: 28px;
}

.showcase__text p {
  margin-bottom: var(--pq-space-6);
}

.showcase__text li {
  position: relative;
  min-height: 32px;
  padding-left: var(--pq-space-6);
  font-size: var(--pq-text-xs);
  letter-spacing: 0.88px;
  line-height: 32px;
  transition: var(--pq-transition);
}

/* La voce scorre a destra al passaggio del mouse, la freccia resta ferma. */
.showcase__text li:hover {
  padding-left: var(--pq-space-8);
}

.showcase__bullet-icon {
  position: absolute;
  top: 0;
  left: 0;
  height: 32px;
  line-height: 32px;
}
</style>
