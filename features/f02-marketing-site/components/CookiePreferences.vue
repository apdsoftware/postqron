<script setup lang="ts">
import { useRequestFetch } from '#imports'
import {
  COOKIE_BANNER_FIRST_LEVEL_ACTIONS,
  COOKIE_CATEGORIES,
  type CookiePreferences,
  type OptionalCookieCategory,
} from '@postqron/compliance'
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'

const STORAGE_KEY = 'postqron.cookie-choice'
const requestFetch = useRequestFetch()
const show = ref(false)
const customize = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const selection = reactive<Record<OptionalCookieCategory, boolean>>({
  preferences: false,
  analytics: false,
  marketing: false,
})

const optionalCategories = COOKIE_CATEGORIES.filter(
  (category): category is OptionalCookieCategory => category !== 'necessary',
)
const categoryLabels: Record<OptionalCookieCategory, {
  title: string
  description: string
}> = {
  preferences: {
    title: 'Preferenze',
    description: 'Ricordano impostazioni che scegli per rendere il sito più comodo.',
  },
  analytics: {
    title: 'Analisi',
    description: 'Aiutano a capire in forma aggregata come viene usato il sito.',
  },
  marketing: {
    title: 'Marketing',
    description: 'Misurano campagne e contenuti promozionali.',
  },
}

function isCurrentChoice(value: string | null): boolean {
  if (!value) {
    return false
  }
  try {
    const parsed = JSON.parse(value) as Pick<CookiePreferences, 'expiresAt'>
    return typeof parsed.expiresAt === 'string'
      && new Date(parsed.expiresAt) > new Date()
  } catch {
    return false
  }
}

async function save(next: Record<OptionalCookieCategory, boolean>) {
  saving.value = true
  errorMessage.value = ''
  try {
    const preferences = await requestFetch<CookiePreferences>('/api/cookie-preferences', {
      method: 'PUT',
      headers: {
        'Idempotency-Key': globalThis.crypto.randomUUID(),
      },
      body: next,
    })
    globalThis.localStorage.setItem(STORAGE_KEY, JSON.stringify(preferences))
    Object.assign(selection, next)
    show.value = false
    customize.value = false
  } catch {
    errorMessage.value = 'Non è stato possibile salvare la scelta. Riprova.'
  } finally {
    saving.value = false
  }
}

function acceptAll() {
  return save({ preferences: true, analytics: true, marketing: true })
}

function rejectAll() {
  return save({ preferences: false, analytics: false, marketing: false })
}

function saveCustom() {
  return save({ ...selection })
}

function openPreferences() {
  show.value = true
  customize.value = true
}

onMounted(() => {
  show.value = !isCurrentChoice(globalThis.localStorage.getItem(STORAGE_KEY))
  globalThis.window.addEventListener('postqron:open-cookie-preferences', openPreferences)
})

onBeforeUnmount(() => {
  globalThis.window.removeEventListener('postqron:open-cookie-preferences', openPreferences)
})
</script>

<template>
  <aside
    v-if="show"
    class="cookie-panel"
    role="region"
    aria-labelledby="cookie-title"
    aria-describedby="cookie-description"
  >
    <div class="cookie-panel__copy">
      <p class="eyebrow">
        La tua scelta
      </p>
      <h2 id="cookie-title">
        Cookie sotto il tuo controllo
      </h2>
      <p id="cookie-description">
        Usiamo sempre solo i cookie necessari. Gli altri restano disattivati
        finché non li accetti.
      </p>
      <NuxtLink to="/legal/cookie">
        Leggi la Cookie Policy
      </NuxtLink>
    </div>

    <div
      v-if="customize"
      class="cookie-panel__options"
    >
      <div class="cookie-option">
        <span>
          <strong>Necessari</strong>
          <small>Servono al funzionamento e non possono essere disattivati.</small>
        </span>
        <span class="cookie-option__status">Sempre attivi</span>
      </div>
      <label
        v-for="category in optionalCategories"
        :key="category"
        class="cookie-option"
      >
        <span>
          <strong>{{ categoryLabels[category].title }}</strong>
          <small>{{ categoryLabels[category].description }}</small>
        </span>
        <input
          v-model="selection[category]"
          type="checkbox"
          :name="category"
        >
      </label>
    </div>

    <p
      v-if="errorMessage"
      class="cookie-panel__error"
      role="alert"
    >
      {{ errorMessage }}
    </p>

    <div class="cookie-panel__actions">
      <button
        class="cookie-action"
        type="button"
        :disabled="saving"
        @click="acceptAll"
      >
        {{ COOKIE_BANNER_FIRST_LEVEL_ACTIONS[0].label }}
      </button>
      <button
        class="cookie-action"
        type="button"
        :disabled="saving"
        @click="rejectAll"
      >
        {{ COOKIE_BANNER_FIRST_LEVEL_ACTIONS[1].label }}
      </button>
      <button
        v-if="!customize"
        class="cookie-action"
        type="button"
        :disabled="saving"
        @click="customize = true"
      >
        {{ COOKIE_BANNER_FIRST_LEVEL_ACTIONS[2].label }}
      </button>
      <button
        v-else
        class="cookie-action"
        type="button"
        :disabled="saving"
        @click="saveCustom"
      >
        Salva preferenze
      </button>
    </div>
  </aside>
</template>
