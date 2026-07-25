<script setup lang="ts">
import { computed, useHead, useRuntimeConfig, useSeoMeta } from '#imports'
import { SUPPORTED_LOCALES, localizeUrl } from '../../f36-i18n/src/index.ts'
import { useMarketingSiteI18n } from '~/locales/runtime.ts'

const config = useRuntimeConfig()
const i18n = useMarketingSiteI18n()
const t = (key: string) => i18n.translate(`marketing-features.${key}`)

const siteUrl = String(config.public.siteUrl).replace(/\/+$/u, '')
const title = computed(() => t('seo.title'))
const description = computed(() => t('seo.description'))
const canonicalPath = computed(() => i18n.localize('/funzionalita'))
const canonical = computed(() => `${siteUrl}${canonicalPath.value}`)

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
      href: `${siteUrl}${localizeUrl(locale, '/funzionalita')}`,
    })),
    {
      rel: 'alternate',
      hreflang: 'x-default',
      href: `${siteUrl}/funzionalita`,
    },
  ],
})))

const capabilities = computed(() => [
  {
    eyebrow: t('cap1.eyebrow'),
    title: t('cap1.title'),
    copy: t('cap1.copy'),
    points: [t('cap1.point1'), t('cap1.point2'), t('cap1.point3')],
  },
  {
    eyebrow: t('cap2.eyebrow'),
    title: t('cap2.title'),
    copy: t('cap2.copy'),
    points: [t('cap2.point1'), t('cap2.point2'), t('cap2.point3')],
  },
  {
    eyebrow: t('cap3.eyebrow'),
    title: t('cap3.title'),
    copy: t('cap3.copy'),
    points: [t('cap3.point1'), t('cap3.point2'), t('cap3.point3')],
  },
])
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

    <section class="capability-list content-wrap">
      <article
        v-for="(capability, index) in capabilities"
        :key="capability.title"
        class="capability"
      >
        <div
          class="capability__visual"
          aria-hidden="true"
        >
          <span>{{ String(index + 1).padStart(2, '0') }}</span>
          <i />
          <i />
          <i />
        </div>
        <div>
          <p class="eyebrow">
            {{ capability.eyebrow }}
          </p>
          <h2>{{ capability.title }}</h2>
          <p>{{ capability.copy }}</p>
          <ul>
            <li
              v-for="point in capability.points"
              :key="point"
            >
              {{ point }}
            </li>
          </ul>
        </div>
      </article>
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
