<script setup lang="ts">
import { computed, useHead, useRuntimeConfig, useSeoMeta } from '#imports'
import { SUPPORTED_LOCALES, localizeUrl } from '../../f36-i18n/src/index.ts'
import FeatureCard from '~/components/FeatureCard.vue'
import PlannerPreview from '~/components/PlannerPreview.vue'
import { useMarketingSiteI18n } from '~/locales/runtime.ts'

const config = useRuntimeConfig()
const i18n = useMarketingSiteI18n()
const t = (key: string, params?: Record<string, string | number>) =>
  i18n.translate(`marketing-home.${key}`, params)

const siteUrl = String(config.public.siteUrl).replace(/\/+$/u, '')
const title = computed(() => t('seo.title'))
const description = computed(() => t('seo.description'))
const canonicalPath = computed(() => i18n.localize('/'))
const canonical = computed(() => `${siteUrl}${canonicalPath.value}`)

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  ogType: 'website',
  ogUrl: canonical,
  ogImage: `${siteUrl}/og.png`,
  twitterCard: 'summary_large_image',
  twitterTitle: title,
  twitterDescription: description,
  twitterImage: `${siteUrl}/og.png`,
})
useHead(computed(() => ({
  link: [
    { rel: 'canonical', href: canonical.value },
    ...SUPPORTED_LOCALES.map(locale => ({
      rel: 'alternate',
      hreflang: locale,
      href: `${siteUrl}${localizeUrl(locale, '/')}`,
    })),
    {
      rel: 'alternate',
      hreflang: 'x-default',
      href: `${siteUrl}/en`,
    },
  ],
  script: [{
    type: 'application/ld+json',
    innerHTML: JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'SoftwareApplication',
      name: 'Postqron',
      applicationCategory: 'BusinessApplication',
      operatingSystem: 'Web',
      description: description.value,
      url: canonical.value,
    }),
  }],
})))

const features = computed(() => [
  {
    number: '01',
    title: t('feature1.title'),
    copy: t('feature1.copy'),
  },
  {
    number: '02',
    title: t('feature2.title'),
    copy: t('feature2.copy'),
  },
  {
    number: '03',
    title: t('feature3.title'),
    copy: t('feature3.copy'),
  },
])
</script>

<template>
  <div>
    <section class="hero-section">
      <div class="content-wrap hero-section__grid">
        <div class="hero-section__copy">
          <p class="eyebrow">
            {{ t('hero.eyebrow') }}
          </p>
          <h1>{{ t('hero.title') }}</h1>
          <p class="hero-section__lead">
            {{ t('hero.lead') }}
          </p>
          <div class="hero-section__actions">
            <a
              class="pq-button"
              :href="config.public.appUrl"
            >{{ t('hero.ctaPrimary') }}</a>
            <NuxtLink
              class="pq-button pq-button--secondary"
              :to="i18n.localize('/funzionalita')"
            >
              {{ t('hero.ctaSecondary') }}
            </NuxtLink>
          </div>
          <p class="hero-section__note">
            {{ t('hero.note') }}
          </p>
        </div>
        <PlannerPreview />
      </div>
    </section>

    <section
      class="trust-strip"
      :aria-label="t('trustStrip.ariaLabel')"
    >
      <div class="content-wrap">
        <span>{{ t('trustStrip.freelance') }}</span>
        <span>{{ t('trustStrip.creators') }}</span>
        <span>{{ t('trustStrip.teams') }}</span>
        <span>{{ t('trustStrip.privacy') }}</span>
      </div>
    </section>

    <section class="section-block content-wrap">
      <div class="section-heading">
        <p class="eyebrow">
          {{ t('features.eyebrow') }}
        </p>
        <h2>{{ t('features.title') }}</h2>
        <p>
          {{ t('features.lead') }}
        </p>
      </div>
      <div class="feature-grid">
        <FeatureCard
          v-for="feature in features"
          :key="feature.number"
          :number="feature.number"
          :title="feature.title"
        >
          {{ feature.copy }}
        </FeatureCard>
      </div>
      <NuxtLink
        class="text-link"
        :to="i18n.localize('/funzionalita')"
      >
        {{ t('features.exploreLink') }} <span aria-hidden="true">→</span>
      </NuxtLink>
    </section>

    <section class="section-block section-block--tinted">
      <div class="content-wrap workflow">
        <div class="section-heading">
          <p class="eyebrow">
            {{ t('workflow.eyebrow') }}
          </p>
          <h2>{{ t('workflow.title') }}</h2>
        </div>
        <ol class="workflow__steps">
          <li>
            <span>1</span>
            <div>
              <h3>{{ t('step1.title') }}</h3>
              <p>{{ t('step1.copy') }}</p>
            </div>
          </li>
          <li>
            <span>2</span>
            <div>
              <h3>{{ t('step2.title') }}</h3>
              <p>{{ t('step2.copy') }}</p>
            </div>
          </li>
          <li>
            <span>3</span>
            <div>
              <h3>{{ t('step3.title') }}</h3>
              <p>{{ t('step3.copy') }}</p>
            </div>
          </li>
        </ol>
      </div>
    </section>

    <section class="cta-band content-wrap">
      <div>
        <p class="eyebrow">
          {{ t('ctaBand.eyebrow') }}
        </p>
        <h2>{{ t('ctaBand.title') }}</h2>
      </div>
      <a
        class="pq-button"
        :href="config.public.appUrl"
      >{{ t('ctaBand.cta') }}</a>
    </section>
  </div>
</template>
