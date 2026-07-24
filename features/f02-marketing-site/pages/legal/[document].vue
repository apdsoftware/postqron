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
import {
  isPublicLegalSlug,
  parsePublishedLegalDocument,
  PUBLIC_LEGAL_DOCUMENTS,
} from '~/src/legal'

const route = useRoute()
const config = useRuntimeConfig()
const slug = String(route.params.document || '')

if (!isPublicLegalSlug(slug)) {
  throw createError({ statusCode: 404, statusMessage: 'Documento non trovato' })
}

const metadata = PUBLIC_LEGAL_DOCUMENTS[slug]
const canonical = `${config.public.siteUrl}/legal/${slug}`
useSeoMeta({
  title: `${metadata.title} — Postqron`,
  description: metadata.description,
  robots: 'index, follow',
  ogTitle: `${metadata.title} — Postqron`,
  ogDescription: metadata.description,
  ogUrl: canonical,
})
useHead({ link: [{ rel: 'canonical', href: canonical }] })

const { data, error, status, refresh } = await useFetch<PublishedLegalDocument>(
  `/api/legal/${slug}`,
  { key: `legal-${slug}` },
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
</script>

<template>
  <div>
    <section class="legal-hero">
      <div class="content-wrap">
        <p class="eyebrow">
          Documenti legali
        </p>
        <h1>{{ metadata.title }}</h1>
        <p>{{ metadata.description }}</p>
        <p
          v-if="document"
          class="legal-hero__version"
        >
          Versione {{ document.version }} · In vigore dal
          {{ new Intl.DateTimeFormat('it-IT', { dateStyle: 'long' }).format(new Date(document.effectiveAt)) }}
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
          {{ status === 'pending' ? 'Caricamento del documento…' : 'Documento non ancora pubblicabile' }}
        </h2>
        <p v-if="error">
          Il contenuto approvato non è disponibile. Postqron non pubblica bozze
          o testi privi dell’approvazione prevista.
        </p>
        <button
          v-if="error"
          class="pq-button pq-button--secondary"
          type="button"
          @click="() => refresh()"
        >
          Riprova
        </button>
      </div>
    </section>
  </div>
</template>
