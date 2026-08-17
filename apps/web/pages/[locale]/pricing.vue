<script setup lang="ts">
import { pricingPages } from '~/content/pricing'
import { isLocaleCode } from '~/utils/locale'

definePageMeta({
  validate: route => isLocaleCode(route.params.locale),
  key: route => `${String(route.params.locale)}:pricing`,
})

const { locale, content, href } = useSiteLocale()
const page = computed(() => pricingPages[locale.value])
const space = '\u00A0'

useLocalizedHead({
  path: '/pricing',
  locale: locale.value,
  title: page.value.meta.title,
  description: page.value.meta.lead,
})

function price(plan: (typeof content.value.plans)[number]) {
  const amount = content.value.money.currencyPosition === 'before'
    ? `${plan.currency}${plan.price}`
    : `${plan.price}${space}${plan.currency}`
  return `${plan.pricePrefix ? `${plan.pricePrefix}${space}` : ''}${amount}${plan.period}`
}

function annualPrice(plan: (typeof content.value.plans)[number]) {
  if (!plan.annual) return ''
  const amount = content.value.money.currencyPosition === 'before'
    ? `${plan.currency}${plan.annual.price}`
    : `${plan.annual.price}${space}${plan.currency}`
  return `${amount}${plan.annual.period}`
}

function staggered(index: number) {
  return { direction: 'bottom' as const, distance: '50px', duration: 0.6, delay: 0.15 * (index + 1) }
}
</script>

<template>
  <main class="pricing-page">
    <PageSection>
      <SectionHeading
        :title="page.intro.title"
        :lead="page.intro.lead"
      />
      <div class="row">
        <div
          v-for="(plan, index) in content.plans"
          :key="plan.name"
          v-reveal="staggered(index)"
          class="col-lg-3 col-md-6 col-sm-12"
        >
          <PricingCard
            :plan="plan"
            :position="index + 1"
            :href="href(plan.ctaTo)"
            :currency-position="content.money.currencyPosition"
            :tax-note="content.money.taxNote"
          />
        </div>
      </div>
      <p class="checkout-note">
        {{ page.checkoutNote }}
      </p>
    </PageSection>

    <PageSection white>
      <SectionHeading
        :title="page.comparisonTitle"
        :lead="page.comparisonLead"
      />
      <div class="comparison-scroll">
        <table class="comparison">
          <caption>{{ page.comparisonTitle }}</caption>
          <thead>
            <tr>
              <th scope="col">
                {{ page.comparisonTitle }}
              </th>
              <th
                v-for="plan in content.plans"
                :key="plan.name"
                scope="col"
              >
                {{ plan.name }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th scope="row">
                {{ page.priceRowLabel }}
              </th>
              <td
                v-for="plan in content.plans"
                :key="plan.name"
              >
                <strong>{{ price(plan) }}</strong>
                <span class="tax-note">{{ content.money.taxNote }}</span>
                <template v-if="plan.annual">
                  <span>{{ annualPrice(plan) }}</span>
                  <span class="tax-note">{{ content.money.taxNote }}</span>
                  <span>{{ plan.annual.savingNote }}</span>
                </template>
              </td>
            </tr>
            <tr
              v-for="row in page.rows"
              :key="row.label"
            >
              <th scope="row">
                {{ row.label }}
              </th>
              <td
                v-for="(value, index) in row.values"
                :key="content.plans[index]!.name"
              >
                <span
                  v-if="value === '✓' || value === '—'"
                  aria-hidden="true"
                  class="comparison__symbol"
                >{{ value }}</span>
                <span
                  v-if="value === '✓'"
                  class="sr-only"
                >{{ page.includedLabel }}</span>
                <span
                  v-else-if="value === '—'"
                  class="sr-only"
                >{{ page.notIncludedLabel }}</span>
                <span v-else>{{ value }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </PageSection>

    <PageSection>
      <div class="downgrade-note">
        <h2>{{ page.downgrade.question }}</h2>
        <p>{{ page.downgrade.answer }}</p>
      </div>
    </PageSection>
  </main>
</template>

<style scoped>
.pricing-page { padding-top: var(--pq-header-height); }
.checkout-note { max-width: 850px; margin: 0 auto; color: var(--pq-text); line-height: 1.7; text-align: center; }
.comparison-scroll { overflow-x: auto; border-radius: var(--pq-radius-lg); box-shadow: var(--pq-shadow-card); }
.comparison { width: 100%; min-width: 960px; border-collapse: collapse; background: var(--pq-surface); color: var(--pq-text); }
.comparison caption { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
.comparison th, .comparison td { padding: var(--pq-space-4); border-bottom: 1px solid var(--pq-border-footer); text-align: left; vertical-align: top; }
.comparison thead th { background: var(--pq-heading); color: var(--pq-text-inverted); }
.comparison tbody th { width: 22%; color: var(--pq-heading); }
.comparison tbody td { width: 19.5%; }
.comparison td > span { display: block; }
.comparison__symbol { color: var(--pq-primary); font-size: var(--pq-text-lg); font-weight: var(--pq-weight-bold); }
.tax-note { color: var(--pq-text-muted); font-size: var(--pq-text-xs); }
.downgrade-note { max-width: 900px; margin: 0 auto; padding: var(--pq-space-8); border-radius: var(--pq-radius-lg); background: var(--pq-surface); box-shadow: var(--pq-shadow-card); }
.downgrade-note h2 { margin-top: 0; color: var(--pq-heading); }
.downgrade-note p { margin-bottom: 0; color: var(--pq-text); line-height: 1.75; }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
</style>
