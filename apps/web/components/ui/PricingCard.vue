<script setup lang="ts">
import type { PricingPlan } from '~/types/content'

defineProps<{
  plan: PricingPlan
  /** Posizione nel listino: è il numero dentro l'esagono. */
  position: number
  /** Destinazione del pulsante, già prefissata con la lingua corrente. */
  href: string
}>()
</script>

<template>
  <div
    class="pricing"
    :class="{ 'is-featured': plan.featured }"
  >
    <header class="pricing__header">
      <h3 class="pricing__name">
        {{ plan.name }}
      </h3>
      <HexagonShape
        :width="48"
        :body="28"
        :cap="14"
        class="pricing__badge"
      >
        <span class="pricing__position">{{ position }}</span>
      </HexagonShape>
    </header>

    <div class="pricing__body">
      <p class="pricing__price">
        <span
          v-if="plan.pricePrefix"
          class="pricing__prefix"
        >{{ plan.pricePrefix }}</span>
        <span class="pricing__currency">{{ plan.currency }}</span>
        <span class="pricing__amount">{{ plan.price }}</span>
        <span class="pricing__period">{{ plan.period }}</span>
      </p>
      <ul class="pricing__features">
        <li
          v-for="feature in plan.features"
          :key="feature.label"
          :class="{ 'is-included': feature.included }"
        >
          {{ feature.label }}
        </li>
      </ul>
    </div>

    <footer class="pricing__footer">
      <LineButton
        :to="href"
        :variant="plan.featured ? 'solid' : 'outline'"
      >
        {{ plan.ctaLabel }}
      </LineButton>
    </footer>
  </div>
</template>

<style scoped>
.pricing {
  margin-bottom: 30px;
  overflow: hidden;
  border-radius: var(--pq-radius);
  background: var(--pq-surface);
  box-shadow: var(--pq-shadow-card);
}

.pricing__header {
  display: block;
  position: relative;
  height: 130px;
  border-bottom: 1px solid var(--pq-border);
  text-align: center;
}

.pricing__name {
  position: absolute;
  top: 50%;
  width: 100%;
  color: var(--pq-heading);
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 1px;
  text-transform: uppercase;
  transform: perspective(1px) translateY(-50%);
}

/*
 * L'esagono è centrato sul bordo inferiore dell'intestazione, che è alta
 * 130px: 101 + 56/2 = 129. Nel tema lo spostamento è 115 perché l'elemento
 * posizionato è la sola fascia centrale e le punte sporgono da lì.
 */
.pricing__badge {
  position: relative;
  bottom: -101px;
  margin-inline: auto;
}

.pricing__position {
  display: block;
  color: var(--pq-primary);
  font-size: 16px;
  line-height: 56px;
  text-align: center;
}

.is-featured .pricing__header {
  background-image: var(--pq-gradient-band);
}

.is-featured .pricing__name {
  color: #fff;
}

.is-featured .pricing__badge {
  --hex-fill: #fff;
  --hex-shadow: drop-shadow(0 2px 24px rgb(0 0 0 / 13%));
}

.pricing__body {
  margin-bottom: 40px;
}

.pricing__price {
  margin-top: 60px;
  margin-bottom: 30px;
  color: var(--pq-primary);
  text-align: center;
}

/*
 * «da», «from», «ab»: sta sulla stessa riga del prezzo, alla quota del simbolo
 * di valuta e nella stessa misura, perché è un qualificatore della cifra e non
 * un'etichetta a sé.
 */
.pricing__prefix {
  position: relative;
  top: -15px;
  margin-right: 4px;
  font-size: 14px;
  font-weight: 500;
}

.pricing__currency {
  position: relative;
  top: -15px;
  font-size: 20px;
  font-weight: 500;
}

.pricing__amount {
  font-size: 34px;
  font-weight: 700;
  letter-spacing: 2.12px;
}

.pricing__period {
  font-size: 14px;
  font-weight: 700;
  letter-spacing: 0.88px;
}

/* Le voci non incluse restano leggibili ma spente. */
.pricing__features li {
  margin-bottom: 12px;
  color: var(--pq-text-disabled);
  font-size: 14px;
  letter-spacing: 0.88px;
  text-align: center;
}

.pricing__features li.is-included {
  color: var(--pq-heading);
}

.pricing__footer {
  padding-bottom: 40px;
  text-align: center;
}
</style>
