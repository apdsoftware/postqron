<script setup lang="ts">
import {
  createError,
  useFetch,
  useHead,
  useRoute,
  useRuntimeConfig,
  useSeoMeta,
} from '#imports'
import type { PublishedLegalDocument } from '@postqron/compliance'
import { computed } from 'vue'
import LegalDocument from '~/components/LegalDocument.vue'
import { SUPPORTED_LOCALES, localizeUrl } from '../../../f36-i18n/src/index.ts'
import { useMarketingSiteI18n } from '~/locales/runtime.ts'
import {
  isPublicLegalSlug,
  parsePublishedLegalDocument,
  PUBLIC_LEGAL_DOCUMENTS,
} from '~/src/legal'

const route = useRoute()
const config = useRuntimeConfig()
const i18n = useMarketingSiteI18n()
const t = (key: string, params?: Record<string, string | number>) =>
  i18n.translate(`marketing-legal.${key}`, params)

const slug = String(route.params.document || '')

if (!isPublicLegalSlug(slug)) {
  throw createError({ statusCode: 404, statusMessage: 'Documento non trovato' })
}

const documentKey = PUBLIC_LEGAL_DOCUMENTS[slug].key
const title = computed(() => t(`doc.${slug}.title`))
const description = computed(() => t(`doc.${slug}.description`))
const siteUrl = String(config.public.siteUrl).replace(/\/+$/u, '')
const canonicalPath = computed(() => i18n.localize(`/legal/${slug}`))
const canonical = computed(() => `${siteUrl}${canonicalPath.value}`)
const seoTitle = computed(() => t('seo.titleTemplate', { title: title.value }))

useSeoMeta({
  title: seoTitle,
  description,
  robots: 'index, follow',
  ogTitle: seoTitle,
  ogDescription: description,
  ogUrl: canonical,
})
useHead(computed(() => ({
  link: [
    { rel: 'canonical', href: canonical.value },
    ...SUPPORTED_LOCALES.map(locale => ({
      rel: 'alternate',
      hreflang: locale,
      href: `${siteUrl}${localizeUrl(locale, `/legal/${slug}`)}`,
    })),
    {
      rel: 'alternate',
      hreflang: 'x-default',
      href: `${siteUrl}/legal/${slug}`,
    },
  ],
})))

const { data, error, status, refresh } = await useFetch<PublishedLegalDocument>(
  `/api/legal/${slug}`,
  {
    key: computed(() => `legal-${slug}-${documentKey}-${i18n.locale.value}`),
    query: computed(() => ({ locale: i18n.locale.value })),
    watch: [i18n.locale],
  },
)
const document = computed(() => {
  if (!data.value) {
    return undefined
  }
  try {
    return parsePublishedLegalDocument(data.value)
  } catch {
    return undefined
  }
})
const versionLabel = computed(() => {
  if (!document.value) {
    return undefined
  }
  const date = i18n.date(document.value.effectiveAt, { dateStyle: 'long' })
  return t('version.label', { version: document.value.version, date })
})
</script>

<template>
  <div>
    <section class="legal-hero">
      <div class="content-wrap">
        <p class="eyebrow">
          {{ t('hero.eyebrow') }}
        </p>
        <h1>{{ title }}</h1>
        <p>{{ description }}</p>
        <p
          v-if="versionLabel"
          class="legal-hero__version"
        >
          {{ versionLabel }}
        </p>
      </div>
    </section>

    <section class="legal-content content-wrap">
      <LegalDocument
        v-if="document"
        :document="document"
      />
      <div
        v-else
        class="catalog-state"
        role="status"
      >
        <h2>
          {{ status === 'pending' ? t('state.loading') : t('state.unavailableTitle') }}
        </h2>
        <p v-if="error">
          {{ t('state.unavailableBody') }}
        </p>
        <button
          v-if="error"
          class="pq-button pq-button--secondary"
          type="button"
          @click="() => refresh()"
        >
          {{ t('state.retry') }}
        </button>
      </div>
    </section>
  </div>
</template>
