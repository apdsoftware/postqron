<script setup lang="ts">
import { useRuntimeConfig } from '#imports'
import { ref } from 'vue'
import {
  formatMoney,
  monthlyPrice,
  type BillingInterval,
  type PublicCatalog,
} from '~/src/catalog'

const props = defineProps<{
  catalog: PublicCatalog
}>()
const config = useRuntimeConfig()
const interval = ref<BillingInterval>('monthly')

function limitLabel(value: number, singular: string, plural: string) {
  return `${new Intl.NumberFormat('it-IT').format(value)} ${value === 1 ? singular : plural}`
}
</script>

<template>
  <div>
    <div
      class="billing-toggle"
      aria-label="Frequenza di fatturazione"
    >
      <button
        type="button"
        :aria-pressed="interval === 'monthly'"
        @click="interval = 'monthly'"
      >
        Mensile
      </button>
      <button
        type="button"
        :aria-pressed="interval === 'annual'"
        @click="interval = 'annual'"
      >
        Annuale
      </button>
    </div>

    <div class="pricing-grid">
      <article
        v-for="plan in props.catalog.plans"
        :key="plan.code"
        class="plan-card"
        :class="{ 'plan-card--featured': plan.code === 'pro' }"
      >
        <span
          v-if="plan.code === 'pro'"
          class="plan-card__badge"
        >Più scelto</span>
        <p class="plan-card__name">
          {{ plan.name }}
        </p>
        <p class="plan-card__price">
          <strong>{{ formatMoney(monthlyPrice(plan, interval)) }}</strong>
          <span>/mese</span>
        </p>
        <p class="plan-card__billing">
          <template v-if="interval === 'annual'">
            {{ formatMoney(plan.prices.annual) }} fatturati ogni anno
          </template>
          <template v-else>
            Fatturazione mensile
          </template>
        </p>
        <a
          class="pq-button"
          :class="{ 'pq-button--secondary': plan.code !== 'pro' }"
          :href="`${config.public.appUrl}?plan=${plan.code}&interval=${interval}`"
        >
          Scegli {{ plan.name }}
        </a>
        <ul>
          <li>{{ limitLabel(plan.limits.members, 'membro', 'membri') }}</li>
          <li>{{ limitLabel(plan.limits.channels, 'canale social', 'canali social') }}</li>
          <li>
            {{ limitLabel(
              plan.limits.scheduled_publications,
              'post programmato',
              'post programmati',
            ) }}
          </li>
        </ul>
      </article>
    </div>
  </div>
</template>
