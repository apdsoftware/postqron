<script setup lang="ts">
import {
  computed,
  useFetch,
  useHead,
  useRoute,
  useRuntimeConfig,
  useSeoMeta,
} from '#imports'
import PlanCatalog from '~/components/PlanCatalog.vue'
import {
  localeFromPath,
  localizePath,
  parsePublicCatalog,
  pricingCopy,
} from '~/src/catalog'

const config = useRuntimeConfig()
const route = useRoute()
const locale = computed(() => localeFromPath(route.fullPath))
const copy = computed(() => pricingCopy(locale.value))
const canonical = computed(() =>
  `${String(config.public.siteUrl).replace(/\/+$/u, '')}${localizePath(locale.value, '/prezzi')}`)

useSeoMeta({
  title: () => copy.value.seoTitle,
  description: () => copy.value.seoDescription,
  ogTitle: () => copy.value.seoTitle,
  ogDescription: () => copy.value.seoDescription,
  ogUrl: () => canonical.value,
  ogImage: () => `${String(config.public.siteUrl).replace(/\/+$/u, '')}/og.png`,
  twitterCard: 'summary_large_image',
})
useHead(() => ({
  link: [
    { rel: 'canonical', href: canonical.value },
    ...(['en', 'it', 'es', 'fr', 'de'] as const).map(language => ({
      rel: 'alternate',
      hreflang: language,
      href: `${String(config.public.siteUrl).replace(/\/+$/u, '')}${localizePath(language, '/prezzi')}`,
    })),
    {
      rel: 'alternate',
      hreflang: 'x-default',
      href: `${String(config.public.siteUrl).replace(/\/+$/u, '')}/en/prezzi`,
    },
  ],
}))

const {
  data: rawCatalog,
  error,
  refresh,
  status,
} = await useFetch('/api/plans', { key: 'public-plan-catalog-d07-v1' })

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
</script>

<template>
  <div>
    <section class="page-hero page-hero--centered content-wrap">
      <p class="eyebrow">
        {{ copy.eyebrow }}
      </p>
      <h1>{{ copy.heroTitle }}</h1>
      <p>{{ copy.heroDescription }}</p>
    </section>

    <section class="pricing-section content-wrap">
      <PlanCatalog
        v-if="catalog"
        :catalog="catalog"
      />
      <div
        v-else
        class="catalog-state"
        :role="error ? 'alert' : 'status'"
        aria-live="polite"
      >
        <h2>
          {{ status === 'pending' ? copy.loading : copy.unavailable }}
        </h2>
        <p v-if="error || status !== 'pending'">
          {{ copy.unavailableDetail }}
        </p>
        <button
          v-if="error || status !== 'pending'"
          class="pq-button pq-button--secondary"
          type="button"
          @click="() => refresh()"
        >
          {{ copy.retry }}
        </button>
      </div>
    </section>

    <section class="pricing-note content-wrap">
      <h2>{{ copy.includedTitle }}</h2>
      <ul>
        <li>{{ copy.includedCalendar }}</li>
        <li>{{ copy.includedDrafts }}</li>
        <li>{{ copy.includedStatus }}</li>
        <li>{{ copy.includedPrivacy }}</li>
      </ul>
      <p>
        {{ copy.faqPrompt }}
        <NuxtLink :to="localizePath(locale, '/faq')">
          {{ copy.faqLink }}
        </NuxtLink>
      </p>
    </section>
  </div>
</template>
