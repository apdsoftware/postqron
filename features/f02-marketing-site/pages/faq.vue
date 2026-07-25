<script setup lang="ts">
import { computed, useHead, useRuntimeConfig, useSeoMeta } from '#imports'
import { SUPPORTED_LOCALES, localizeUrl } from '../../f36-i18n/src/index.ts'
import { useMarketingSiteI18n } from '~/locales/runtime.ts'

const config = useRuntimeConfig()
const i18n = useMarketingSiteI18n()
const t = (key: string) => i18n.translate(`marketing-faq.${key}`)

const siteUrl = String(config.public.siteUrl).replace(/\/+$/u, '')
const title = computed(() => t('seo.title'))
const description = computed(() => t('seo.description'))
const canonicalPath = computed(() => i18n.localize('/faq'))
const canonical = computed(() => `${siteUrl}${canonicalPath.value}`)

const questions = computed(() => [
  { question: t('q1.question'), answer: t('q1.answer') },
  { question: t('q2.question'), answer: t('q2.answer') },
  { question: t('q3.question'), answer: t('q3.answer') },
  { question: t('q4.question'), answer: t('q4.answer') },
  { question: t('q5.question'), answer: t('q5.answer') },
  { question: t('q6.question'), answer: t('q6.answer') },
])

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  ogUrl: canonical,
  ogImage: `${siteUrl}/og.png`,
  twitterCard: 'summary_large_image',
})
useHead(computed(() => ({
  link: [
    { rel: 'canonical', href: canonical.value },
    ...SUPPORTED_LOCALES.map(locale => ({
      rel: 'alternate',
      hreflang: locale,
      href: `${siteUrl}${localizeUrl(locale, '/faq')}`,
    })),
    {
      rel: 'alternate',
      hreflang: 'x-default',
      href: `${siteUrl}/faq`,
    },
  ],
  script: [{
    type: 'application/ld+json',
    innerHTML: JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'FAQPage',
      mainEntity: questions.value.map(item => ({
        '@type': 'Question',
        name: item.question,
        acceptedAnswer: {
          '@type': 'Answer',
          text: item.answer,
        },
      })),
    }),
  }],
})))
</script>

<template>
  <div>
    <section class="page-hero content-wrap">
      <p class="eyebrow">
        {{ t('hero.eyebrow') }}
      </p>
      <h1>{{ t('hero.title') }}</h1>
      <p>
        {{ t('hero.lead') }}
      </p>
    </section>

    <section class="faq-list content-wrap">
      <details
        v-for="(item, index) in questions"
        :key="item.question"
        :open="index === 0"
      >
        <summary>
          <span>{{ item.question }}</span>
          <span aria-hidden="true">+</span>
        </summary>
        <p>{{ item.answer }}</p>
      </details>
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
