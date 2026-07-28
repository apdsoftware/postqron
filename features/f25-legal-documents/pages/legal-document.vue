<script setup lang="ts">
import {
  createError,
  useRoute,
  useSeoMeta,
} from '#imports'
import {
  computed,
  defineComponent,
  h,
  type PropType,
  type VNodeChild,
} from 'vue'
import { usePostqronI18n } from '../../f36-i18n/runtime.ts'
import {
  DOCUMENT_TYPES,
  isDocumentType,
  loadBundledRepository,
  parseLegalMarkdown,
  type DocumentType,
  type LegalInline,
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
  dpa: {
    en: 'Data Processing Agreement',
    it: 'Accordo sul trattamento dei dati',
    es: 'Acuerdo de tratamiento de datos',
    fr: 'Accord de traitement des données',
    de: 'Auftragsverarbeitungsvertrag',
  },
  subprocessors: {
    en: 'Subprocessor registry',
    it: 'Registro dei sub-responsabili',
    es: 'Registro de subencargados',
    fr: 'Registre des sous-traitants',
    de: 'Unterauftragsverarbeiter-Register',
  },
}
const blockedMessages: Record<LegalLocale, string> = {
  en: 'This legal document is not published because legal review is incomplete.',
  it: 'Questo documento legale non è pubblicato perché la revisione legale non è completa.',
  es: 'Este documento legal no está publicado porque la revisión legal no está completa.',
  fr: 'Ce document juridique n’est pas publié car la révision juridique n’est pas terminée.',
  de: 'Dieses Rechtsdokument ist nicht veröffentlicht, da die rechtliche Prüfung noch nicht abgeschlossen ist.',
}
const externalLinkMessages: Record<LegalLocale, string> = {
  en: 'opens in a new tab',
  it: 'si apre in una nuova scheda',
  es: 'se abre en una pestaña nueva',
  fr: 's’ouvre dans un nouvel onglet',
  de: 'wird in einem neuen Tab geöffnet',
}
const tableMessages: Record<LegalLocale, string> = {
  en: 'Scrollable legal document table',
  it: 'Tabella scorrevole del documento legale',
  es: 'Tabla desplazable del documento legal',
  fr: 'Tableau défilable du document juridique',
  de: 'Scrollbare Tabelle des Rechtsdokuments',
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

function renderInline(nodes: LegalInline[]): VNodeChild[] {
  return nodes.map(node => {
    switch (node.type) {
      case 'text':
        return node.value
      case 'code':
        return h('code', node.value)
      case 'strong':
        return h('strong', renderInline(node.children))
      case 'emphasis':
        return h('em', renderInline(node.children))
      case 'link':
        return h('a', {
          href: node.href,
          ...(node.external
            ? {
                target: '_blank',
                rel: 'noopener noreferrer',
                'aria-label': `${node.children.map(child =>
                  child.type === 'text' ? child.value : '').join('')} (${externalLinkMessages[i18n.locale.value]})`,
              }
            : {}),
        }, renderInline(node.children))
    }
  })
}

const LegalMarkdown = defineComponent({
  props: {
    content: {
      type: String as PropType<string>,
      required: true,
    },
  },
  setup(props) {
    const blocks = computed(() => parseLegalMarkdown(props.content))
    return () => blocks.value.flatMap((block, index) => {
      switch (block.type) {
        case 'heading':
          // The approved corpus starts with an H1 matching the page title.
          // The article header above already owns the single document H1.
          return block.level === 1
            ? []
            : [h(`h${block.level}`, { key: index }, renderInline(block.children))]
        case 'thematicBreak':
          return [h('hr', { key: index })]
        case 'paragraph':
          return [h('p', { key: index }, renderInline(block.children))]
        case 'list':
          return [h(block.ordered ? 'ol' : 'ul', { key: index },
            block.items.map((item, itemIndex) =>
              h('li', { key: itemIndex }, renderInline(item))))]
        case 'table':
          return [h('div', {
            key: index,
            class: 'legal-release__table',
            role: 'region',
            tabindex: '0',
            'aria-label': `${tableMessages[i18n.locale.value]} ${index + 1}`,
          }, [
            h('table', [
              h('thead', [
                h('tr', block.header.map((cell, cellIndex) =>
                  h('th', { key: cellIndex, scope: 'col' }, renderInline(cell)))),
              ]),
              h('tbody', block.rows.map((row, rowIndex) =>
                h('tr', { key: rowIndex }, row.map((cell, cellIndex) =>
                  h('td', { key: cellIndex }, renderInline(cell))))),
              ),
            ]),
          ])]
      }
    })
  },
})
</script>

<template>
  <div class="legal-release content-wrap">
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
        <LegalMarkdown :content="published.content" />
      </div>
    </article>
  </div>
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
  line-height: var(--pq-line-height-body);
}

.legal-release__content :deep(h2) {
  margin-block: var(--pq-space-8) var(--pq-space-3);
}

.legal-release__content :deep(h3) {
  margin-block: var(--pq-space-6) var(--pq-space-2);
}

.legal-release__content :deep(p),
.legal-release__content :deep(ul),
.legal-release__content :deep(ol) {
  margin-block: var(--pq-space-3);
}

.legal-release__content :deep(li + li) {
  margin-block-start: var(--pq-space-2);
}

.legal-release__content :deep(a) {
  overflow-wrap: anywhere;
}

.legal-release__content :deep(code) {
  white-space: break-spaces;
  overflow-wrap: anywhere;
}

.legal-release__content :deep(.legal-release__table) {
  max-width: 100%;
  margin-block: var(--pq-space-5);
  overflow-x: auto;
  overscroll-behavior-inline: contain;
}

.legal-release__content :deep(table) {
  width: 100%;
  min-width: 42rem;
  border-collapse: collapse;
}

.legal-release__content :deep(th),
.legal-release__content :deep(td) {
  padding: var(--pq-space-2) var(--pq-space-3);
  border: 1px solid currentcolor;
  text-align: start;
  vertical-align: top;
}

.legal-release__content :deep(th) {
  font-weight: var(--pq-font-weight-bold);
}

@media (max-width: 36rem) {
  .legal-release dl div {
    grid-template-columns: 1fr;
  }
}
</style>
