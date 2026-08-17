<script setup lang="ts">
defineProps<{
  /** Nome di chi parla. */
  name: string
  /** Ruolo e organizzazione, sulla riga sotto. */
  role: string
  quote: string
  avatar: string
  /** Testo alternativo del ritratto, già composto da `ui.photoOf`. */
  photoAlt: string
  /**
   * Citazione inventata. Il markup lo espone come `data-placeholder`: è così
   * che il percorso di deploy (#426) può accorgersene senza leggere i testi.
   */
  placeholder: boolean
}>()
</script>

<template>
  <div
    class="testimonial"
    :data-placeholder="placeholder ? 'true' : undefined"
  >
    <div class="testimonial__avatar">
      <HexagonAvatar
        :src="avatar"
        :alt="photoAlt"
      />
    </div>
    <blockquote class="testimonial__body">
      <!--
        L'attribuzione precede la citazione perché è così che il tema la
        dispone; resta dentro il `footer` del `blockquote`, che è il posto
        previsto per il riferimento della citazione.
      -->
      <footer>
        <cite class="testimonial__name">{{ name }}</cite>
        <span class="testimonial__role">{{ role }}</span>
      </footer>
      <p class="testimonial__quote">
        {{ quote }}
      </p>
    </blockquote>
  </div>
</template>

<style scoped>
.testimonial {
  display: block;
  position: relative;
  margin-bottom: var(--pq-space-6);
}

.testimonial__avatar {
  position: absolute;
  top: -10px;
  left: 25px;
  z-index: 3;
  transition: var(--pq-transition);
}

.testimonial:hover .testimonial__avatar {
  top: -30px;
}

.testimonial__body {
  position: relative;
  overflow: hidden;
  border-radius: var(--pq-radius);
  background: var(--pq-surface-soft);
  box-shadow: var(--pq-shadow-card);
}

.testimonial__body::before {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 1;
  opacity: 0;
  background-image: var(--pq-gradient);
  transition: var(--pq-transition);
}

.testimonial:hover .testimonial__body::before {
  opacity: 1;
}

.testimonial__name,
.testimonial__role,
.testimonial__quote {
  position: relative;
  z-index: 3;
  display: block;
}

/* Il rientro di 140px lascia posto all'esagono in sovrimpressione. */
.testimonial__name {
  margin-top: var(--pq-space-4);
  margin-bottom: var(--pq-space-1);
  padding-left: var(--pq-space-28);
  color: var(--pq-heading);
  font-size: var(--pq-text-base);
  font-style: normal;

  /* Nel tema il nome è un titolo: ne conserva l'interlinea, non quella del corpo. */
  line-height: 1.2;
  letter-spacing: 0.69px;
  transition: var(--pq-transition);
}

.testimonial__role {
  padding-right: var(--pq-space-5);
  padding-left: var(--pq-space-28);
  color: var(--pq-text);
  font-size: var(--pq-text-xs);
  letter-spacing: 0.6px;
  transition: var(--pq-transition);
}

.testimonial__quote {
  margin-top: var(--pq-space-8);
  margin-bottom: var(--pq-space-5);
  padding-right: var(--pq-space-5);
  padding-left: var(--pq-space-5);
  color: var(--pq-text);
  font-size: var(--pq-text-xs);
  letter-spacing: 0.6px;
  line-height: 26px;
  transition: var(--pq-transition);
}

.testimonial:hover .testimonial__name {
  color: var(--pq-text-inverted);
}

.testimonial:hover .testimonial__role,
.testimonial:hover .testimonial__quote {
  color: var(--pq-text-on-gradient);
}
</style>
