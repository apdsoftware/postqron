<script setup lang="ts">
import {
  computed,
  definePageMeta,
  useFetch,
  useHead,
  useRuntimeConfig,
  useSeoMeta,
} from '#imports'
import {
  SUPPORTED_LOCALES,
  localizeUrl,
} from '../../f36-i18n/src/index.ts'
import { parsePublicCatalog } from '../../f02-marketing-site/src/catalog.ts'
import PrelaunchPricing from '../components/PrelaunchPricing.vue'
import { usePrelaunch } from '../runtime.ts'
import { PRELAUNCH_CATALOGS } from '../src/catalogs.ts'

definePageMeta({ layout: 'prelaunch' })

const prelaunch = usePrelaunch()
const runtimeConfig = useRuntimeConfig()
const siteUrl = String(runtimeConfig.public.siteUrl).replace(/\/+$/u, '')
const canonicalPath = computed(() =>
  localizeUrl(prelaunch.locale.value, '/prelaunch'))
const canonicalUrl = computed(() => `${siteUrl}${canonicalPath.value}`)
const title = computed(() => prelaunch.translate('landing.metaTitle'))
const description = computed(() =>
  prelaunch.translate('landing.metaDescription'))
const accessUrl = computed(() =>
  localizeUrl(prelaunch.locale.value, '/prelaunch/access'))
const {
  data: rawCatalog,
  status: catalogStatus,
} = await useFetch('/api/plans', {
  key: 'prelaunch-public-plan-catalog-d09-v1',
})
const catalog = computed(() => {
  if (!rawCatalog.value) {
    return undefined
  }
  try {
    return parsePublicCatalog(rawCatalog.value)
  } catch {
    return undefined
  }
})

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  ogType: 'website',
  ogUrl: canonicalUrl,
  ogImage: `${siteUrl}/brand/social-card.svg`,
  twitterCard: 'summary_large_image',
})
useHead(computed(() => ({
  link: [
    { rel: 'canonical', href: canonicalUrl.value },
    ...SUPPORTED_LOCALES.map(locale => ({
      rel: 'alternate',
      hreflang: locale,
      href: `${siteUrl}${localizeUrl(locale, '/prelaunch')}`,
      title: PRELAUNCH_CATALOGS[locale]['landing.title'],
    })),
    {
      rel: 'alternate',
      hreflang: 'x-default',
      href: `${siteUrl}/en/prelaunch`,
    },
  ],
})))

const valueCards = computed(() => [
  {
    number: '01',
    title: prelaunch.translate('landing.valuePlan'),
    copy: prelaunch.translate('landing.valuePlanCopy'),
  },
  {
    number: '02',
    title: prelaunch.translate('landing.valueAdapt'),
    copy: prelaunch.translate('landing.valueAdaptCopy'),
  },
  {
    number: '03',
    title: prelaunch.translate('landing.valueKnow'),
    copy: prelaunch.translate('landing.valueKnowCopy'),
  },
])
</script>

<template>
  <div class="prelaunch-page">
    <section class="prelaunch-hero">
      <div class="prelaunch-hero__copy">
        <p class="prelaunch-status">
          <span aria-hidden="true" />
          {{ prelaunch.translate('status.label') }}
        </p>
        <p class="eyebrow">
          {{ prelaunch.translate('landing.eyebrow') }}
        </p>
        <h1>{{ prelaunch.translate('landing.title') }}</h1>
        <p class="prelaunch-hero__lead">
          {{ prelaunch.translate('landing.description') }}
        </p>
        <div class="prelaunch-hero__action">
          <NuxtLink
            class="pq-button"
            :to="accessUrl"
            :prefetch="false"
          >
            {{ prelaunch.translate('landing.cta') }}
          </NuxtLink>
          <p>{{ prelaunch.translate('landing.note') }}</p>
        </div>
      </div>

      <div
        class="prelaunch-orbit"
        aria-hidden="true"
      >
        <div class="prelaunch-orbit__calendar">
          <span />
          <span />
          <span />
          <span />
          <span />
          <span />
        </div>
        <div class="prelaunch-orbit__badge prelaunch-orbit__badge--one">
          ✓
        </div>
        <div class="prelaunch-orbit__badge prelaunch-orbit__badge--two">
          12:30
        </div>
      </div>
    </section>

    <section
      class="prelaunch-values"
      aria-label="Postqron"
    >
      <article
        v-for="card in valueCards"
        :key="card.number"
      >
        <span>{{ card.number }}</span>
        <h2>{{ card.title }}</h2>
        <p>{{ card.copy }}</p>
      </article>
    </section>

    <PrelaunchPricing
      v-if="catalog"
      :catalog="catalog"
      :access-url="accessUrl"
    />
    <section
      v-else
      class="prelaunch-pricing-state"
      aria-live="polite"
    >
      <p class="eyebrow">
        {{ prelaunch.translate('pricing.eyebrow') }}
      </p>
      <h2>{{ prelaunch.translate('pricing.title') }}</h2>
      <p>
        {{ catalogStatus === 'pending'
          ? prelaunch.translate('pricing.loading')
          : prelaunch.translate('pricing.unavailable') }}
      </p>
      <NuxtLink
        class="pq-button"
        :to="accessUrl"
        :prefetch="false"
      >
        {{ prelaunch.translate('pricing.cta') }}
      </NuxtLink>
    </section>
  </div>
</template>

<style scoped>
.prelaunch-page {
  width: min(calc(100% - clamp(1.25rem, 5vw, 2rem)), 72rem);
  margin-inline: auto;
  padding: clamp(2.5rem, 7vw, 6rem) 0 clamp(4rem, 9vw, 8rem);
}

.prelaunch-hero {
  display: grid;
  grid-template-columns: minmax(0, 1.12fr) minmax(18rem, 0.88fr);
  gap: clamp(2rem, 7vw, 7rem);
  align-items: center;
}

.prelaunch-status {
  display: inline-flex;
  align-items: center;
  gap: 0.55rem;
  margin: 0 0 2rem;
  border: 1px solid var(--pq-pine-300);
  border-radius: 999px;
  padding: 0.45rem 0.8rem;
  color: var(--pq-pine-800);
  background: #ffffffb3;
  font-size: var(--pq-font-size-sm);
  font-weight: var(--pq-font-weight-semibold);
}

.prelaunch-status span {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 50%;
  background: var(--pq-coral-500);
  box-shadow: 0 0 0 0.25rem var(--pq-coral-100);
}

.prelaunch-hero h1 {
  max-width: 13ch;
  margin: 0;
  font-size: var(--pq-font-size-4xl);
  line-height: var(--pq-line-height-tight);
  letter-spacing: var(--pq-letter-spacing-tight);
}

.prelaunch-hero__lead {
  max-width: 58ch;
  margin: 1.5rem 0 0;
  color: var(--pq-color-text-muted);
  font-size: clamp(1.05rem, 2vw, 1.25rem);
  line-height: 1.65;
}

.prelaunch-hero__action {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem 1.25rem;
  align-items: center;
  margin-top: 2rem;
}

.prelaunch-hero__action p {
  max-width: 25ch;
  margin: 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
}

.prelaunch-orbit {
  position: relative;
  aspect-ratio: 1;
  border: 1px solid var(--pq-pine-200);
  border-radius: 50%;
  background: #dceee466;
}

.prelaunch-orbit::before,
.prelaunch-orbit::after {
  position: absolute;
  border: 1px solid var(--pq-pine-200);
  border-radius: 50%;
  content: "";
  inset: 12%;
}

.prelaunch-orbit::after {
  inset: 29%;
}

.prelaunch-orbit__calendar {
  position: absolute;
  z-index: 1;
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 0.65rem;
  width: 54%;
  border: 1px solid var(--pq-color-border);
  border-radius: 1.25rem;
  padding: 1.2rem;
  background: #fff;
  box-shadow: var(--pq-shadow-lg);
  inset: 23%;
}

.prelaunch-orbit__calendar span {
  min-height: 2rem;
  border-radius: 0.45rem;
  background: var(--pq-pine-100);
}

.prelaunch-orbit__calendar span:nth-child(2),
.prelaunch-orbit__calendar span:nth-child(5) {
  background: var(--pq-coral-100);
}

.prelaunch-orbit__badge {
  position: absolute;
  z-index: 2;
  display: grid;
  min-width: 3rem;
  min-height: 3rem;
  place-items: center;
  border-radius: 1rem;
  color: #fff;
  background: var(--pq-pine-800);
  box-shadow: var(--pq-shadow-md);
  font-weight: var(--pq-font-weight-bold);
}

.prelaunch-orbit__badge--one {
  top: 12%;
  right: 12%;
}

.prelaunch-orbit__badge--two {
  bottom: 12%;
  left: 5%;
  padding-inline: 0.85rem;
  color: var(--pq-pine-950);
  background: var(--pq-coral-300);
}

.prelaunch-values {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1px;
  margin-top: clamp(4rem, 10vw, 9rem);
  overflow: hidden;
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-xl);
  background: var(--pq-color-border);
}

.prelaunch-values article {
  padding: clamp(1.5rem, 4vw, 2.5rem);
  background: #fff;
}

.prelaunch-values article > span {
  color: var(--pq-color-accent);
  font-size: var(--pq-font-size-sm);
  font-weight: var(--pq-font-weight-bold);
}

.prelaunch-values h2 {
  margin: 2rem 0 0.75rem;
  font-size: var(--pq-font-size-xl);
}

.prelaunch-values p {
  margin: 0;
  color: var(--pq-color-text-muted);
  line-height: var(--pq-line-height-body);
}

.prelaunch-pricing-state {
  max-width: 48rem;
  margin: clamp(4rem, 10vw, 8rem) auto 0;
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-xl);
  padding: clamp(1.5rem, 5vw, 3rem);
  background: #fff;
  text-align: center;
}

.prelaunch-pricing-state h2 {
  margin: 0;
  font-size: var(--pq-font-size-2xl);
}

.prelaunch-pricing-state > p:not(.eyebrow) {
  color: var(--pq-color-text-muted);
}

@media (max-width: 48rem) {
  .prelaunch-hero {
    grid-template-columns: 1fr;
  }

  .prelaunch-orbit {
    width: min(100%, 26rem);
    margin-inline: auto;
  }

  .prelaunch-values {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 30rem) {
  .prelaunch-page {
    padding-top: 1.75rem;
  }

  .prelaunch-hero h1 {
    overflow-wrap: anywhere;
    font-size: clamp(2.25rem, 13vw, var(--pq-font-size-4xl));
  }

  .prelaunch-hero__action,
  .prelaunch-hero__action .pq-button {
    width: 100%;
  }
}

@media (prefers-reduced-motion: reduce) {
  .prelaunch-status span {
    box-shadow: none;
  }
}
</style>
