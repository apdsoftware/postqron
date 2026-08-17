<script setup lang="ts">
import type { RasterImageName } from '~/utils/images'

defineProps<{
  title: string
  excerpt: string
  /** Nome della copertina nel registro delle immagini. */
  image: RasterImageName
  /** Destinazione già prefissata con la lingua corrente. */
  to: string
  /** Invito in fondo alla card: arriva da `ui.readMore`. */
  ctaLabel: string
}>()

/*
 * Larghezza di rendering della copertina, soglia per soglia: sono le colonne
 * che `layout.css` produce per `col-lg-4 col-md-6 col-sm-12` dentro i
 * contenitori di Bootstrap (540 / 720 / 960 / 1140 / 1320). Senza questo, il
 * browser assume `100vw` e su un portatile scarica la variante da 800px per
 * mostrarla in una colonna da 296.
 */
const COVER_SIZES = [
  '(min-width: 1400px) 416px',
  '(min-width: 1200px) 356px',
  '(min-width: 992px) 296px',
  '(min-width: 768px) 336px',
  '(min-width: 576px) 516px',
  'calc(100vw - 24px)',
].join(', ')
</script>

<template>
  <article class="article-card">
    <!--
      Il tema ritaglia le copertine con un plugin jQuery che misura e
      riposiziona ogni immagine. `object-fit: cover` fa lo stesso lavoro nel
      compositore, e continua a funzionare al ridimensionamento della finestra.
    -->
    <NuxtLink
      :to="to"
      class="article-card__figure"
      tabindex="-1"
      aria-hidden="true"
    >
      <ResponsiveImage
        :name="image"
        alt=""
        :sizes="COVER_SIZES"
      />
    </NuxtLink>
    <h3 class="article-card__title">
      <NuxtLink :to="to">{{ title }}</NuxtLink>
    </h3>
    <p class="article-card__excerpt">
      {{ excerpt }}
    </p>
    <LineButton
      :to="to"
      class="article-card__cta"
    >
      {{ ctaLabel }}
    </LineButton>
  </article>
</template>

<style scoped>
.article-card {
  margin-bottom: var(--pq-space-6);
  text-align: center;
}

.article-card__figure {
  display: block;
  height: 200px;
  margin-bottom: var(--pq-space-5);
  overflow: hidden;
  border-radius: var(--pq-radius);
}

/*
 * `:deep` perché fra la figura e l'immagine c'è ora il `<picture>` di
 * `ResponsiveImage`, che è lui a portare l'attributo di ambito. Le proporzioni
 * native della copertina e quelle del riquadro non coincidono: è `object-fit`
 * a decidere il ritaglio, mentre `width`/`height` sull'`<img>` restano quelle
 * vere del file e servono al browser prima che il CSS arrivi.
 */
.article-card__figure :deep(img) {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

/*
 * Il titolo tiene la dimensione predefinita di un `h3` (28px) mentre il link
 * al suo interno scende a 16px: è la combinazione del tema, e da lì viene
 * l'interlinea generosa delle righe del titolo.
 */
.article-card__title {
  margin-bottom: var(--pq-space-2);
  font-size: var(--pq-text-2xl);
}

.article-card__title a {
  color: var(--pq-heading);
  font-size: var(--pq-text-base);
  font-weight: var(--pq-weight-regular);
  letter-spacing: 1px;
  line-height: 26px;
  transition: var(--pq-transition);
}

.article-card__title a:hover {
  color: var(--pq-primary);
}

.article-card__excerpt {
  margin-bottom: var(--pq-space-3);
  color: var(--pq-text);
  font-size: var(--pq-text-xs);
  letter-spacing: 0.88px;
  line-height: 26px;
}

.article-card__cta {
  margin-bottom: var(--pq-space-8);
}
</style>
