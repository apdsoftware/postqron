<script setup lang="ts">
import {
  createError,
  useRoute,
  useSeoMeta,
} from '#imports'
import { computed } from 'vue'
import { usePostqronI18n } from '../../f36-i18n/runtime.ts'
import {
  DOCUMENT_TYPES,
  isDocumentType,
  loadBundledRepository,
  type DocumentType,
  type LegalLocale,
} from '../src/index.ts'

const route = useRoute()
const i18n = usePostqronI18n()
const pathSegments = computed(() => route.path.split('/').filter(Boolean))
const legalSegmentIndex = computed(() => pathSegments.value.indexOf('legal'))
const slug = computed(() =>
  String(pathSegments.value[legalSegmentIndex.value + 1] || ''))
const exactVersion = computed(() =>
  pathSegments.value[legalSegmentIndex.value + 2])

if (!isDocumentType(slug.value)) {
  throw createError({ statusCode: 404, statusMessage: 'Legal document not found' })
}

const document = slug.value as DocumentType
const titles: Record<DocumentType, Record<LegalLocale, string>> = {
  terms: {
    en: 'Terms and conditions',
    it: 'Termini e condizioni',
    es: 'Términos y condiciones',
    fr: 'Conditions générales',
    de: 'Allgemeine Geschäftsbedingungen',
  },
  privacy: {
    en: 'Privacy Policy',
    it: 'Informativa sulla privacy',
    es: 'Política de privacidad',
    fr: 'Politique de confidentialité',
    de: 'Datenschutzerklärung',
  },
  cookies: {
    en: 'Cookie Policy',
    it: 'Cookie Policy',
    es: 'Política de cookies',
    fr: 'Politique relative aux cookies',
    de: 'Cookie-Richtlinie',
  },
}
const blockedMessages: Record<LegalLocale, string> = {
  en: 'This legal document is not published because legal review is incomplete.',
  it: 'Questo documento legale non è pubblicato perché la revisione legale non è completa.',
  es: 'Este documento legal no está publicado porque la revisión legal no está completa.',
  fr: 'Ce document juridique n’est pas publié car la révision juridique n’est pas terminée.',
  de: 'Dieses Rechtsdokument ist nicht veröffentlicht, da die rechtliche Prüfung noch nicht abgeschlossen ist.',
}

const repository = await loadBundledRepository()
if (!repository.ready) {
  throw createError({
    statusCode: 503,
    statusMessage: 'Legal release blocked',
    message: blockedMessages[i18n.locale.value],
    data: {
      code: 'legal_release_blocked',
      requiredDocuments: DOCUMENT_TYPES,
    },
  })
}

const published = exactVersion.value
  ? repository.version(document, exactVersion.value, i18n.locale.value)
  : repository.current(document, i18n.locale.value)
if (!published) {
  throw createError({
    statusCode: 503,
    statusMessage: 'Legal release not effective',
    message: blockedMessages[i18n.locale.value],
    data: { code: 'legal_release_not_effective' },
  })
}

useSeoMeta({
  title: `${published.title || titles[document][i18n.locale.value]} — Postqron`,
  robots: 'noindex, nofollow',
})
</script>

<template>
  <main class="legal-release content-wrap">
    <article>
      <header>
        <p class="eyebrow">
          {{ published.document }}
        </p>
        <h1>{{ published.title }}</h1>
        <dl>
          <div>
            <dt>Version</dt>
            <dd>{{ published.version }}</dd>
          </div>
          <div>
            <dt>Effective date</dt>
            <dd>{{ published.effectiveAt }}</dd>
          </div>
          <div>
            <dt>Digest</dt>
            <dd><code>sha256:{{ published.digestSha256 }}</code></dd>
          </div>
        </dl>
      </header>
      <div class="legal-release__content">
        {{ published.content }}
      </div>
    </article>
  </main>
</template>

<style scoped>
.legal-release {
  max-width: 76ch;
  padding-block: clamp(3rem, 8vw, 7rem);
}

.legal-release dl {
  display: grid;
  gap: var(--pq-space-2);
  margin-block: var(--pq-space-6);
}

.legal-release dl div {
  display: grid;
  grid-template-columns: minmax(8rem, 0.25fr) minmax(0, 0.75fr);
  gap: var(--pq-space-3);
}

.legal-release dt {
  font-weight: var(--pq-font-weight-bold);
}

.legal-release dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.legal-release__content {
  white-space: pre-wrap;
  line-height: var(--pq-line-height-body);
}

@media (max-width: 36rem) {
  .legal-release dl div {
    grid-template-columns: 1fr;
  }
}
</style>
