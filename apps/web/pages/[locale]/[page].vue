<script setup lang="ts">
import { publicPages } from '~/content/pages'
import { isLocaleCode, localePath } from '~/utils/locale'
import { isPublicPageId } from '~/utils/public-pages'
import { canonicalUrl } from '~/utils/site'
import { faqPageNode, organizationNode } from '~/utils/structured-data'

definePageMeta({
  validate: route => isLocaleCode(route.params.locale) && isPublicPageId(route.params.page),
  key: route => `${String(route.params.locale)}:${String(route.params.page)}`,
})

const route = useRoute()
const { locale, content } = useSiteLocale()
const pageId = computed(() => {
  if (!isPublicPageId(route.params.page)) throw createError({ statusCode: 404 })
  return route.params.page
})
const page = computed(() => publicPages[locale.value][pageId.value])
const featuresPage = computed(() => publicPages[locale.value].features)
const faqPage = computed(() => publicPages[locale.value].faq)
const contactPage = computed(() => publicPages[locale.value].contact)

useLocalizedHead({
  path: `/${pageId.value}`,
  locale: locale.value,
  title: page.value.meta.title,
  description: page.value.meta.lead,
})

/*
 * Dati strutturati per pagina, e solo dove il contenuto li sostiene (R53-ter).
 * La rotta ha una `key` per parametro, quindi `setup` riparte a ogni cambio di
 * pagina e questi rami vengono rivalutati: niente resta appeso altrove.
 *
 * - **FAQ:** le domande e le risposte dichiarate sono le stesse che il
 *   `<details>` qui sotto rende, parola per parola — `mainEntity` e contenuto
 *   visibile hanno un'unica sorgente.
 * - **Contatti:** è la pagina che mostra ragione sociale, indirizzo, partita
 *   IVA e recapito, cioè esattamente i campi di `Organization`.
 * - **Funzionalità:** niente. Non c'è un tipo schema.org che descriva un elenco
 *   di funzionalità senza dire anche qualcosa che qui non diciamo.
 */
const { public: config } = useRuntimeConfig()

if (pageId.value === 'faq') {
  useStructuredData([
    faqPageNode(
      faqPage.value.items,
      canonicalUrl(localePath('/faq', locale.value), config.siteUrl),
    ),
  ])
}

if (pageId.value === 'contact') {
  useStructuredData([organizationNode(content.value, locale.value, config.siteUrl)])
}

function staggered(index: number) {
  return { direction: 'bottom' as const, distance: '50px', duration: 0.6, delay: 0.15 * (index + 1) }
}
</script>

<template>
  <main class="content-page">
    <PageSection>
      <SectionHeading
        :title="page.intro.title"
        :lead="page.intro.lead"
      />

      <template v-if="pageId === 'features'">
        <div class="row">
          <div
            v-for="(feature, index) in featuresPage.features"
            :key="feature.title"
            v-reveal="staggered(index)"
            class="col-lg-3 col-md-6 col-sm-6 col-12"
          >
            <FeatureCard v-bind="feature" />
          </div>
        </div>
      </template>

      <div
        v-else-if="pageId === 'faq'"
        class="faq-list"
      >
        <details
          v-for="item in faqPage.items"
          :key="item.question"
          class="faq-item"
        >
          <summary>{{ item.question }}</summary>
          <p>{{ item.answer }}</p>
        </details>
      </div>

      <div
        v-else-if="pageId === 'contact'"
        class="contact-card"
      >
        <dl>
          <div
            v-for="detail in contactPage.details"
            :key="detail.label"
          >
            <dt>{{ detail.label }}</dt>
            <dd>
              <a
                v-if="detail.href"
                :href="detail.href"
              >{{ detail.value }}</a>
              <span v-else>{{ detail.value }}</span>
            </dd>
          </div>
        </dl>
        <p>{{ contactPage.responseNote }}</p>
      </div>
    </PageSection>

    <template v-if="pageId === 'features'">
      <PageSection
        v-for="(showcase, index) in featuresPage.showcases"
        :key="showcase.title"
        white
        tight
        :divider="index === 0"
        :watermark="index % 2 === 0 ? 'right' : 'left'"
      >
        <ShowcaseBlock v-bind="showcase" />
      </PageSection>
    </template>
  </main>
</template>

<style scoped>
.content-page {
  padding-top: var(--pq-header-height);
}

.faq-list,
.contact-card {
  max-width: 900px;
  margin: 0 auto;
}

.faq-item {
  margin-bottom: var(--pq-space-4);
  border: 1px solid var(--pq-border-footer);
  border-radius: var(--pq-radius-lg);
  background: var(--pq-surface);
  box-shadow: var(--pq-shadow-card);
}

.faq-item summary {
  padding: var(--pq-space-5) var(--pq-space-6);
  color: var(--pq-heading);
  font-weight: var(--pq-weight-bold);
  cursor: pointer;
}

.faq-item p {
  margin: 0;
  padding: 0 var(--pq-space-6) var(--pq-space-6);
  color: var(--pq-text);
  line-height: 1.75;
}

.contact-card {
  padding: var(--pq-space-8);
  border-radius: var(--pq-radius-lg);
  background: var(--pq-surface);
  box-shadow: var(--pq-shadow-card);
}

.contact-card dl {
  margin: 0;
}

.contact-card dl div {
  padding: var(--pq-space-4) 0;
  border-bottom: 1px solid var(--pq-border-footer);
}

.contact-card dt {
  margin-bottom: var(--pq-space-1);
  color: var(--pq-heading);
  font-weight: var(--pq-weight-bold);
}

.contact-card dd {
  margin: 0;
  color: var(--pq-text);
}

.contact-card a {
  color: var(--pq-primary);
  text-decoration: underline;
}

.contact-card > p {
  margin: var(--pq-space-6) 0 0;
  color: var(--pq-text);
  line-height: 1.75;
}
</style>
