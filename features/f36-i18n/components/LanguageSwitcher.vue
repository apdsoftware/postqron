<script setup lang="ts">
import { computed, ref, useRoute } from '#imports'
import {
  FOUNDATION_CATALOGS,
  SUPPORTED_LOCALES,
  createLanguageSwitcherModel,
  isLocale,
  translateCatalog,
  type Locale,
} from '../src/index.ts'
import { usePostqronI18n } from '../runtime'

const i18n = usePostqronI18n()
const route = useRoute()
const announcement = ref('')
const model = computed(() =>
  createLanguageSwitcherModel(i18n.locale.value, route.fullPath))

function labelFor(locale: Locale): string {
  return model.value.items.find(item => item.locale === locale)?.label ?? locale
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

async function onChange(event: { currentTarget: unknown }): Promise<void> {
  const target = event.currentTarget as { value?: unknown } | null
  if (target && isLocale(target.value)) {
    await choose(target.value)
  }
}
</script>

<template>
  <div
    class="postqron-language-switcher"
  >
    <label class="postqron-language-switcher__control">
      <span
        class="postqron-language-switcher__icon"
        aria-hidden="true"
      >◎</span>
      <span class="postqron-language-switcher__label">
        {{ model.label }}
      </span>
      <select
        :aria-label="model.label"
        :value="i18n.locale.value"
        @change="onChange"
      >
        <option
          v-for="locale in SUPPORTED_LOCALES"
          :key="locale"
          :value="locale"
          :lang="locale"
        >
          {{ labelFor(locale) }}
        </option>
      </select>
    </label>
    <p
      aria-live="polite"
      class="postqron-language-switcher__status"
      role="status"
    >
      {{ announcement }}
    </p>
  </div>
</template>

<style scoped>
.postqron-language-switcher {
  min-width: 0;
}

.postqron-language-switcher__control {
  position: relative;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  min-height: var(--pq-size-target-min, 2.75rem);
  overflow: hidden;
  border: 1px solid var(--pq-color-border, #cbd8d1);
  border-radius: var(--pq-radius-full, 999px);
  color: var(--pq-color-text, #153328);
  background: color-mix(in srgb, var(--pq-color-surface, #fff) 92%, transparent);
  box-shadow: 0 0.25rem 1rem #1533280d;
}

.postqron-language-switcher__icon {
  z-index: 1;
  margin-left: 0.85rem;
  color: var(--pq-color-brand, #185c43);
  font-size: 1.1rem;
  pointer-events: none;
}

.postqron-language-switcher__label {
  block-size: 1px;
  clip-path: inset(50%);
  inline-size: 1px;
  overflow: hidden;
  position: absolute;
  white-space: nowrap;
}

.postqron-language-switcher select {
  min-width: 0;
  min-height: inherit;
  border: 0;
  padding: 0.55rem 2.25rem 0.55rem 0.55rem;
  color: inherit;
  background: transparent;
  font: inherit;
  font-size: var(--pq-font-size-sm, 0.875rem);
  font-weight: var(--pq-font-weight-semibold, 600);
  cursor: pointer;
}

.postqron-language-switcher select:focus {
  outline: 0;
}

.postqron-language-switcher__control:focus-within {
  outline: var(--pq-border-focus, 2px) solid var(--pq-color-focus, #0b6cff);
  outline-offset: 3px;
}

.postqron-language-switcher__control:hover {
  border-color: var(--pq-color-brand, #185c43);
  background: var(--pq-color-surface, #fff);
}

@media (max-width: 30rem) {
  .postqron-language-switcher__icon {
    margin-left: 0.65rem;
  }

  .postqron-language-switcher select {
    max-width: 8.25rem;
    padding-right: 1.75rem;
  }
}

@media (forced-colors: active) {
  .postqron-language-switcher__control {
    border: 1px solid CanvasText;
  }
}

.postqron-language-switcher__status {
  block-size: 1px;
  clip-path: inset(50%);
  inline-size: 1px;
  overflow: hidden;
  margin: 0;
  position: absolute;
  white-space: nowrap;
}
</style>
