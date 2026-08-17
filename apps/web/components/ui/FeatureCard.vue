<script setup lang="ts">
import { NuxtLink } from '#components'

import type { HexIconName } from '~/utils/icons'

const props = withDefaults(
  defineProps<{
    icon: HexIconName
    title: string
    text: string
    /** Destinazione della card. Senza, la card non è cliccabile. */
    to?: string
    /** Mostra la card già nello stato "evidenziato", come al passaggio del mouse. */
    highlighted?: boolean
  }>(),
  { to: undefined },
)

// Una card senza destinazione non deve diventare un link vuoto.
const tag = computed(() => (props.to ? NuxtLink : 'div'))
</script>

<template>
  <component
    :is="tag"
    :to="to"
    class="feature-card"
    :class="{ 'is-highlighted': highlighted }"
  >
    <div class="feature-card__icon">
      <HexagonShape
        :width="60"
        :body="37"
        :cap="15"
      />
      <span class="feature-card__glyph"><HexIcon :name="icon" /></span>
    </div>
    <h3 class="feature-card__title">
      {{ title }}
    </h3>
    <p class="feature-card__text">
      {{ text }}
    </p>
  </component>
</template>

<style scoped>
.feature-card {
  display: block;
  position: relative;
  margin-bottom: 30px;
  padding: 40px 28px;
  overflow: hidden;
  border-radius: var(--pq-radius);
  background: var(--pq-surface-soft);
  box-shadow: var(--pq-shadow-card);
  text-align: center;
  transition: var(--pq-transition);
}

/*
 * Il gradiente sta su uno strato separato invece che sul fondo della card:
 * un `background-image` non è animabile, l'opacità sì.
 */
.feature-card::before {
  content: '';
  position: absolute;
  inset: 0;
  opacity: 0;
  background-image: var(--pq-gradient);
  transition: var(--pq-transition);
}

.feature-card:hover,
.feature-card.is-highlighted {
  --hex-fill: #fff;
}

.feature-card:hover::before,
.feature-card.is-highlighted::before {
  opacity: 1;
}

.feature-card:hover .feature-card__title,
.feature-card.is-highlighted .feature-card__title {
  color: #fff;
}

.feature-card:hover .feature-card__text,
.feature-card.is-highlighted .feature-card__text {
  color: var(--pq-text-on-gradient);
}

.feature-card:hover {
  margin-top: -15px;
}

/*
 * 35px sotto l'esagono, non 20: nel tema il margine superiore della fascia
 * centrale sfugge dal contenitore per collasso e il distacco dal titolo
 * risulta più ampio di quanto la regola lasci intendere.
 */
.feature-card__icon {
  position: relative;
  width: 60px;
  height: 67px;
  margin: 0 auto 35px;
}

/* Il glifo è sovrapposto all'esagono e alto quanto lui, quindi ne è il centro. */
.feature-card__glyph {
  display: block;
  position: absolute;
  top: 0;
  width: 100%;
  height: 67px;
  z-index: 2;
  color: var(--pq-primary);
  font-size: 18px;
  line-height: 67px;
  text-align: center;
}

.feature-card__title {
  position: relative;
  z-index: 2;
  margin-bottom: 15px;
  color: var(--pq-heading);
  font-size: 16px;
  font-weight: 400;
  letter-spacing: 0.7px;
  transition: var(--pq-transition);
}

.feature-card__text {
  position: relative;
  z-index: 2;
  color: var(--pq-text);
  font-size: 14px;
  letter-spacing: 0.88px;
  line-height: 26px;
  transition: var(--pq-transition);
}
</style>
