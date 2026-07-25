<script setup lang="ts">
import { computed, ref, useRoute } from '#imports'
import {
  FOUNDATION_CATALOGS,
  SUPPORTED_LOCALES,
  createLanguageSwitcherModel,
  translateCatalog,
  type LanguageSwitcherItem,
  type Locale,
} from '../src/index.ts'
import { usePostqronI18n } from '../runtime'

const i18n = usePostqronI18n()
const route = useRoute()
const announcement = ref('')
const model = computed(() =>
  createLanguageSwitcherModel(i18n.locale.value, route.fullPath))

function itemFor(locale: Locale): LanguageSwitcherItem | undefined {
  return model.value.items.find(item => item.locale === locale)
}

async function choose(locale: Locale): Promise<void> {
  await i18n.setLocale(locale, route.fullPath)
  const language = translateCatalog(
    FOUNDATION_CATALOGS[locale],
    locale,
    `language.${locale}`,
  )
  announcement.value = translateCatalog(
    FOUNDATION_CATALOGS[locale],
    locale,
    'languageSwitcher.changed',
    { language },
  )
}
</script>

<template>
  <nav
    class="postqron-language-switcher"
    :aria-label="model.label"
  >
    <ul class="postqron-language-switcher__list">
      <li
        v-for="locale in SUPPORTED_LOCALES"
        :key="locale"
      >
        <a
          :aria-current="locale === i18n.locale.value ? 'page' : undefined"
          :href="itemFor(locale)?.href"
          :hreflang="locale"
          :lang="locale"
          @click.prevent="choose(locale)"
        >
          {{ itemFor(locale)?.label }}
        </a>
      </li>
    </ul>
    <p
      aria-live="polite"
      class="postqron-language-switcher__status"
      role="status"
    >
      {{ announcement }}
    </p>
  </nav>
</template>

<style scoped>
.postqron-language-switcher__list {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  list-style: none;
  margin: 0;
  padding: 0;
}

.postqron-language-switcher a[aria-current="page"] {
  font-weight: 700;
  text-decoration-thickness: 0.15em;
}

.postqron-language-switcher a:focus-visible {
  outline: 0.2rem solid currentColor;
  outline-offset: 0.2rem;
}

.postqron-language-switcher__status {
  block-size: 1px;
  clip-path: inset(50%);
  inline-size: 1px;
  overflow: hidden;
  position: absolute;
  white-space: nowrap;
}
</style>
