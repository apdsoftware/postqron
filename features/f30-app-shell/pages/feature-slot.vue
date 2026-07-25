<script setup lang="ts">
import { definePageMeta } from '#imports'
import { useAppShellI18n } from '../components/core/use-app-shell.ts'

definePageMeta({ layout: 'app-shell' })
const { t } = useAppShellI18n()
</script>

<template>
  <section class="app-feature-page">
    <p class="app-eyebrow">
      {{ t('feature.title') }}
    </p>
    <h1>{{ t('state.empty.title') }}</h1>
    <p>{{ t('feature.description') }}</p>
    <div
      class="app-slot"
      data-postqron-slot="feature-content"
    >
      <AppState kind="empty" />
    </div>
  </section>
</template>
