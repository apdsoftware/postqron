<script setup lang="ts">
import { computed, useRoute } from '#imports'
import {
  FOUNDATION_CATALOGS,
  SUPPORTED_LOCALES,
  translateCatalog,
  type Locale,
} from '../../f36-i18n/src/index.ts'
import { usePostqronI18n } from '../../f36-i18n/runtime'
import { useAdminI18n } from '../core/use-admin.ts'

const i18n = usePostqronI18n()
const route = useRoute()
const { t } = useAdminI18n()

const options = computed(() => SUPPORTED_LOCALES.map(locale => ({
  value: locale,
  label: translateCatalog(FOUNDATION_CATALOGS[locale], locale, `language.${locale}`),
})))

async function change(event: unknown) {
  const value = (event as { target?: { value?: string } }).target?.value as Locale | undefined
  if (!value || value === i18n.locale.value) {
    return
  }
  await i18n.setLocale(value, route.fullPath)
}
</script>

<template>
  <label class="admin-language-select">
    <span class="pq-visually-hidden">{{ t('shell.language') }}</span>
    <select
      :value="i18n.locale.value"
      @change="change"
    >
      <option
        v-for="option in options"
        :key="option.value"
        :value="option.value"
      >
        {{ option.label }}
      </option>
    </select>
  </label>
</template>
