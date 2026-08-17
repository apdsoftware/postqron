<script setup lang="ts">
defineProps<{
  title: string
  excerpt: string
  image: string
  /** Destinazione già prefissata con la lingua corrente. */
  to: string
  /** Invito in fondo alla card: arriva da `ui.readMore`. */
  ctaLabel: string
}>()
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
      <img
        :src="image"
        alt=""
        class="article-card__image"
        loading="lazy"
      >
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

.article-card__image {
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
