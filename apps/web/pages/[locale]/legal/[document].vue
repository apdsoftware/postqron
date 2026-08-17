<script setup lang="ts">
import { isLocaleCode } from '~/utils/locale'
// `isLegalDocumentId` arriva da `legal-documents` e non da `legal`: la `validate`
// qui sotto viene estratta nel manifesto delle rotte, che sta nel bundle
// d'ingresso di ogni pagina. Importarla da `legal` porterebbe `marked` e i
// quattro Markdown sulla home.
import { isLegalDocumentId } from '~/utils/legal-documents'
import { legalDocument } from '~/utils/legal'

definePageMeta({
  validate: route => isLocaleCode(route.params.locale) && isLegalDocumentId(route.params.document),
  key: route => `${String(route.params.locale)}:${String(route.params.document)}`,
})

const route = useRoute()
const { locale, content } = useSiteLocale()
const documentId = computed(() => {
  if (!isLegalDocumentId(route.params.document)) throw createError({ statusCode: 404 })
  return route.params.document
})
const document = computed(() => legalDocument(documentId.value, locale.value))

useLocalizedHead({
  path: `/legal/${documentId.value}`,
  locale: locale.value,
  title: document.value.title,
  description: document.value.title,
})
</script>

<template>
  <PageSection white>
    <article
      class="legal-document"
      lang="en"
    >
      <p
        v-if="locale !== document.language"
        class="legal-document__notice"
        role="note"
      >
        {{ content.legal.sourceNotice }}
      </p>

      <h1>{{ document.title }}</h1>
      <dl class="legal-document__metadata">
        <div>
          <dt>{{ content.legal.versionLabel }}</dt>
          <dd>{{ document.version }}</dd>
        </div>
        <div>
          <dt>{{ content.legal.effectiveDateLabel }}</dt>
          <dd><time :datetime="document.effectiveDate">{{ document.effectiveDate }}</time></dd>
        </div>
      </dl>

      <!-- eslint-disable vue/no-v-html -->
      <!-- Il markup deriva esclusivamente dai file legali versionati e controllati. -->
      <div
        class="legal-document__body"
        v-html="document.html"
      />
      <!-- eslint-enable vue/no-v-html -->
    </article>
  </PageSection>
</template>

<style scoped>
.legal-document {
  max-width: 900px;
  margin: var(--pq-space-14) auto 0;
  color: var(--pq-text);
}

.legal-document h1 {
  margin-bottom: var(--pq-space-5);
  color: var(--pq-heading);
  font-size: var(--pq-text-3xl);
  line-height: 1.2;
}

.legal-document__notice {
  margin-bottom: var(--pq-space-6);
  padding: var(--pq-space-4) var(--pq-space-5);
  border-left: 4px solid var(--pq-primary);
  background: var(--pq-surface-tint);
  color: var(--pq-heading);
  font-weight: var(--pq-weight-medium);
}

.legal-document__metadata {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pq-space-4) var(--pq-space-10);
  margin-bottom: var(--pq-space-10);
  padding: var(--pq-space-4) 0;
  border-top: 1px solid var(--pq-border-footer);
  border-bottom: 1px solid var(--pq-border-footer);
}

.legal-document__metadata div {
  display: flex;
  gap: var(--pq-space-2);
}

.legal-document__metadata dt {
  color: var(--pq-heading);
  font-weight: var(--pq-weight-bold);
}

.legal-document__metadata dd {
  margin: 0;
}

.legal-document__body :deep(h2),
.legal-document__body :deep(h3) {
  margin-top: var(--pq-space-10);
  margin-bottom: var(--pq-space-4);
  color: var(--pq-heading);
  line-height: 1.3;
}

.legal-document__body :deep(p),
.legal-document__body :deep(li) {
  line-height: 1.75;
}

.legal-document__body :deep(p),
.legal-document__body :deep(ul),
.legal-document__body :deep(ol),
.legal-document__body :deep(table) {
  margin-bottom: var(--pq-space-5);
}

.legal-document__body :deep(ul),
.legal-document__body :deep(ol) {
  padding-left: var(--pq-space-6);
}

.legal-document__body :deep(a) {
  color: var(--pq-primary);
  text-decoration: underline;
}

.legal-document__body :deep(table) {
  width: 100%;
  border-collapse: collapse;
}

.legal-document__body :deep(th),
.legal-document__body :deep(td) {
  padding: var(--pq-space-3);
  border: 1px solid var(--pq-border-footer);
  text-align: left;
  vertical-align: top;
}

.legal-document__body :deep(th) {
  background: var(--pq-surface-tint);
  color: var(--pq-heading);
}

@media (max-width: 767px) {
  .legal-document {
    margin-top: var(--pq-space-10);
  }

  .legal-document h1 {
    font-size: var(--pq-text-2xl);
  }

  .legal-document__body {
    overflow-wrap: anywhere;
  }
}
</style>
