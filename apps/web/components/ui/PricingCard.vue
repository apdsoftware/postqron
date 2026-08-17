<script setup lang="ts">
import type { MoneyFormat, PricingPlan } from '~/types/content'

const props = defineProps<{
  plan: PricingPlan
  /** Posizione nel listino: è il numero dentro l'esagono. */
  position: number
  /** Destinazione del pulsante, già prefissata con la lingua corrente. */
  href: string
  /**
   * Dove va il simbolo di valuta nella lingua corrente. L'importo è lo stesso
   * in tutte e cinque (R61); a cambiare è solo la convenzione di scrittura.
   */
  currencyPosition: MoneyFormat['currencyPosition']
  /**
   * Indicazione dell'imposta nella lingua corrente (R61-bis). È obbligatoria:
   * una cifra senza è un difetto, e un valore predefinito qui sarebbe una
   * stringa scritta nel componente.
   */
  taxNote: string
}>()

/**
 * Fra cifra e simbolo posposto va uno spazio unificatore: «9 €» è un'unica
 * quantità e non deve spezzarsi a fine riga. Non è testo da tradurre — è la
 * punteggiatura della regola qui sopra — quindi vive nel componente.
 *
 * Scritto come sequenza di escape perché a occhio è indistinguibile da uno
 * spazio normale, e la differenza è tutto il punto.
 */
const NON_BREAKING_SPACE = '\u00A0'

const trailingCurrency = computed(() => `${NON_BREAKING_SPACE}${props.plan.currency}`)

const annualPrice = computed(() => {
  if (!props.plan.annual) return ''
  const amount = props.currencyPosition === 'before'
    ? `${props.plan.currency}${props.plan.annual.price}`
    : `${props.plan.annual.price}${NON_BREAKING_SPACE}${props.plan.currency}`
  return `${amount}${props.plan.annual.period}`
})

/**
 * Anche fra qualificatore e cifra lo spazio \u00E8 nel testo, non solo nel margine:
 * un margine si vede ma non si copia, e \u00ABab79 \u20AC\u00BB \u00E8 ci\u00F2 che finisce negli
 * appunti di chi seleziona il prezzo.
 */
const leadingPrefix = computed(() =>
  props.plan.pricePrefix ? `${props.plan.pricePrefix}${NON_BREAKING_SPACE}` : '',
)
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
      <!--
        Due regole locali diverse sulla stessa riga: dove va il simbolo e come
        si chiama l'imposta. «€9/month excluding tax» in inglese, «9 €/mese imposte escluse» in
        italiano — nessuna delle due parti è scritta qui.
      -->
      <p class="pricing__price">
        <span
          v-if="plan.pricePrefix"
          class="pricing__prefix"
          :class="{ 'pricing__prefix--baseline': currencyPosition === 'after' }"
        >{{ leadingPrefix }}</span>
        <span
          v-if="currencyPosition === 'before'"
          class="pricing__currency"
        >{{ plan.currency }}</span>
        <span class="pricing__amount">{{ plan.price }}</span>
        <span
          v-if="currencyPosition === 'after'"
          class="pricing__currency pricing__currency--after"
        >{{ trailingCurrency }}</span>
        <span class="pricing__period">{{ plan.period }}</span>
      </p>
      <p class="pricing__tax">
        {{ taxNote }}
      </p>
      <p
        v-if="plan.annual"
        class="pricing__annual"
      >
        <span>{{ annualPrice }}</span>
        <span>{{ taxNote }}</span>
        <strong>{{ plan.annual.savingNote }}</strong>
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
  margin-bottom: var(--pq-space-6);
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
  font-size: var(--pq-text-base);
  font-weight: var(--pq-weight-bold);
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
  font-size: var(--pq-text-base);
  line-height: 56px;
  text-align: center;
}

.is-featured .pricing__header {
  background-image: var(--pq-gradient-band);
}

.is-featured .pricing__name {
  color: var(--pq-text-inverted);
}

.is-featured .pricing__badge {
  --hex-fill: var(--pq-text-inverted);
  --hex-shadow: var(--pq-shadow-hex);
}

.pricing__body {
  margin-bottom: var(--pq-space-8);
}

.pricing__price {
  margin-top: var(--pq-space-12);
  margin-bottom: 8px;
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
  font-size: var(--pq-text-xs);
  font-weight: var(--pq-weight-medium);
}

/*
 * Il qualificatore si allinea a ciò che lo segue. Con il simbolo anteposto
 * precede un `€` sollevato e sta bene in alto; con il simbolo in coda precede
 * la cifra grande, e restare sollevato lo farebbe sembrare un richiamo a una
 * nota — «ab» sospeso sopra il 79.
 */
.pricing__prefix--baseline {
  top: 0;
}

.pricing__currency {
  position: relative;
  top: -15px;
  font-size: var(--pq-text-lg);
  font-weight: var(--pq-weight-medium);
}

.pricing__amount {
  font-size: var(--pq-text-3xl);
  font-weight: var(--pq-weight-bold);
  letter-spacing: 2.12px;
}

/*
 * Il simbolo posposto sta sulla riga di scrittura, non in alto: sollevato
 * sembrerebbe un richiamo a una nota invece che parte della cifra.
 */
.pricing__currency--after {
  top: 0;
}

.pricing__period {
  font-size: var(--pq-text-xs);
  font-weight: var(--pq-weight-bold);
  letter-spacing: 0.88px;
}

/*
 * L'imposta va sotto, su una riga propria: accanto al prezzo com'è richiesto,
 * ma senza contendere spazio alla cifra dentro una card da 306px — «zzgl. Steuern»
 * dopo «ab 79 €/Monat» non ci starebbe in nessun caso.
 *
 * È un paragrafo a sé e non uno `span` dentro quello del prezzo: è
 * un'affermazione distinta sulla cifra, e da elemento separato il testo
 * selezionato non esce come «/Monat zzgl. Steuern».
 */
.pricing__tax {
  margin-bottom: var(--pq-space-6);
  color: var(--pq-text);
  font-size: var(--pq-text-2xs);
  font-weight: var(--pq-weight-medium);
  letter-spacing: 0.75px;
  text-align: center;
}

.pricing__annual {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 0 var(--pq-space-2);
  margin: 0 var(--pq-space-4) var(--pq-space-4);
  color: var(--pq-text);
  font-size: var(--pq-text-xs);
  text-align: center;
}

.pricing__annual strong {
  flex-basis: 100%;
  color: var(--pq-primary);
}

/* Le voci non incluse restano leggibili ma spente. */
.pricing__features li {
  margin-bottom: 12px;
  color: var(--pq-text-disabled);
  font-size: var(--pq-text-xs);
  letter-spacing: 0.88px;
  text-align: center;
}

.pricing__features li.is-included {
  color: var(--pq-heading);
}

.pricing__footer {
  padding-bottom: var(--pq-space-8);
  text-align: center;
}
</style>
