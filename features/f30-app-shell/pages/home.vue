<script setup lang="ts">
import { computed, definePageMeta } from '#imports'
import {
  useAppSessionState,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: 'app-shell' })

const session = useAppSessionState()
const { t } = useAppShellI18n()
const displayName = computed(() =>
  session.value?.account.display_name || t('shell.profile'))
</script>

<template>
  <section class="app-home">
    <p class="app-eyebrow">
      {{ t('home.eyebrow') }}
    </p>
    <h1>{{ t('home.welcome', { name: displayName }) }}</h1>
    <p class="app-home__lead">
      {{ t('home.description') }}
    </p>

    <div
      class="app-home__summary"
      data-postqron-slot="home-summary"
    >
      <article>
        <span aria-hidden="true">◎</span>
        <p>{{ t('home.getStarted') }}</p>
      </article>
    </div>
    <div class="app-home__grid">
      <section
        class="app-slot"
        data-postqron-slot="home-primary"
        :aria-label="t('state.empty.title')"
      >
        <AppState kind="empty" />
      </section>
      <section
        class="app-slot"
        data-postqron-slot="home-secondary"
        :aria-label="t('state.empty.title')"
      >
        <AppState kind="empty" />
      </section>
    </div>
  </section>
</template>
