<script setup lang="ts">
import {
  computed,
  useHead,
  useRuntimeConfig,
  useSeoMeta,
} from '#imports'
import {
  SUPPORTED_LOCALES,
  localizeUrl,
} from '../../f36-i18n/src/index.ts'
import { useSupportContact } from '../runtime.ts'

const support = useSupportContact()
const runtimeConfig = useRuntimeConfig()
const siteUrl = String(runtimeConfig.public.siteUrl).replace(/\/+$/u, '')
const canonicalPath = computed(() => support.localize('/contatti'))
const canonicalUrl = computed(() => `${siteUrl}${canonicalPath.value}`)
const title = computed(() => support.translate('page.metaTitle'))
const description = computed(() => support.translate('page.metaDescription'))
const email = computed(() => support.config.value.email)
const responseBusinessDays = computed(() =>
  support.config.value.responseBusinessDays)

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  ogUrl: canonicalUrl,
  ogImage: `${siteUrl}/og.png`,
  robots: 'index, follow',
  twitterCard: 'summary_large_image',
})
useHead(computed(() => ({
  link: [
    { rel: 'canonical', href: canonicalUrl.value },
    ...SUPPORTED_LOCALES.map(locale => ({
      rel: 'alternate',
      hreflang: locale,
      href: `${siteUrl}${localizeUrl(locale, '/contatti')}`,
    })),
    {
      rel: 'alternate',
      hreflang: 'x-default',
      href: `${siteUrl}/contatti`,
    },
  ],
  script: [{
    type: 'application/ld+json',
    innerHTML: JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'ContactPage',
      name: title.value,
      description: description.value,
      url: canonicalUrl.value,
      mainEntity: {
        '@type': 'Organization',
        name: 'Postqron',
        email: email.value,
      },
    }),
  }],
})))
</script>

<template>
  <div class="support-contact">
    <section class="page-hero content-wrap">
      <p class="eyebrow">
        {{ support.translate('page.eyebrow') }}
      </p>
      <h1>{{ support.translate('page.title') }}</h1>
      <p>{{ support.translate('page.description') }}</p>
    </section>

    <section
      class="support-contact__channels content-wrap"
      :aria-label="support.translate('page.emailHeading')"
    >
      <article class="support-contact__primary">
        <p class="eyebrow">
          {{ support.translate('page.emailHeading') }}
        </p>
        <p>{{ support.translate('page.emailDescription') }}</p>
        <a
          class="pq-button"
          :href="support.mailto()"
          :aria-label="support.translate('page.emailLinkLabel', { email })"
        >
          {{ email }}
        </a>
      </article>

      <article class="support-contact__timing">
        <h2>{{ support.translate('page.responseHeading') }}</h2>
        <p>
          {{ support.translate('page.responseTiming', {
            count: responseBusinessDays,
          }) }}
        </p>
        <p>{{ support.translate('page.responseNote') }}</p>
      </article>
    </section>

    <section class="support-contact__guidance content-wrap">
      <div>
        <p class="eyebrow">
          {{ support.translate('page.includeHeading') }}
        </p>
        <h2>{{ support.translate('page.includeIntro') }}</h2>
      </div>
      <ul>
        <li>{{ support.translate('page.includeAccount') }}</li>
        <li>{{ support.translate('page.includeSummary') }}</li>
        <li>{{ support.translate('page.includeSteps') }}</li>
        <li>{{ support.translate('page.includeEvidence') }}</li>
      </ul>
    </section>

    <aside class="support-contact__safety content-wrap">
      <h2>{{ support.translate('page.safetyHeading') }}</h2>
      <p>{{ support.translate('page.safetyDescription') }}</p>
    </aside>
  </div>
</template>

<style scoped>
.support-contact {
  padding-bottom: clamp(4rem, 9vw, 7rem);
}

.support-contact__channels {
  display: grid;
  grid-template-columns: minmax(0, 1.15fr) minmax(18rem, 0.85fr);
  gap: var(--pq-space-5);
  margin-bottom: clamp(4rem, 9vw, 7rem);
}

.support-contact__channels article {
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-xl);
  padding: clamp(1.5rem, 5vw, 3rem);
}

.support-contact__primary {
  color: var(--pq-color-text-inverse);
  background: var(--pq-pine-900);
}

.support-contact__primary .eyebrow {
  color: var(--pq-coral-300);
}

.support-contact__primary p:not(.eyebrow) {
  max-width: 42ch;
  color: var(--pq-pine-200);
}

.support-contact__primary .pq-button {
  margin-top: var(--pq-space-6);
  color: var(--pq-pine-950);
  background: var(--pq-coral-300);
  overflow-wrap: anywhere;
}

.support-contact__timing {
  background: var(--pq-color-surface);
}

.support-contact__timing h2,
.support-contact__guidance h2,
.support-contact__safety h2 {
  margin-top: 0;
  line-height: var(--pq-line-height-heading);
}

.support-contact__timing p,
.support-contact__guidance li,
.support-contact__safety p {
  color: var(--pq-color-text-muted);
}

.support-contact__guidance {
  display: grid;
  grid-template-columns: minmax(16rem, 0.75fr) minmax(0, 1.25fr);
  gap: clamp(2rem, 8vw, 7rem);
  align-items: start;
}

.support-contact__guidance ul {
  display: grid;
  gap: var(--pq-space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}

.support-contact__guidance li {
  position: relative;
  padding: var(--pq-space-4) var(--pq-space-5) var(--pq-space-4) 3.5rem;
  border-bottom: 1px solid var(--pq-color-border);
}

.support-contact__guidance li::before {
  position: absolute;
  top: var(--pq-space-4);
  left: var(--pq-space-4);
  color: var(--pq-color-brand);
  font-weight: var(--pq-font-weight-bold);
  content: "✓";
}

.support-contact__safety {
  margin-top: clamp(3rem, 7vw, 5rem);
  border-left: 0.35rem solid var(--pq-color-accent);
  border-radius: var(--pq-radius-md);
  padding: var(--pq-space-6);
  background: var(--pq-color-brand-subtle);
}

.support-contact__safety p {
  max-width: 72ch;
  margin-bottom: 0;
}

@media (max-width: 48rem) {
  .support-contact__channels,
  .support-contact__guidance {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 36rem) {
  .support-contact__primary .pq-button {
    width: 100%;
  }

  .support-contact__guidance li {
    padding-right: var(--pq-space-2);
  }
}

@media (forced-colors: active) {
  .support-contact__channels article,
  .support-contact__safety {
    border: 2px solid CanvasText;
  }
}
</style>
