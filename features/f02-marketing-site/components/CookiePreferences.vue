<script setup lang="ts">
/* global HTMLElement, KeyboardEvent, Navigator, StorageEvent, Window */
import { useRequestFetch } from '#imports'
import {
  COOKIE_BANNER_FIRST_LEVEL_ACTIONS,
  COOKIE_CATEGORIES,
  type OptionalCookieCategory,
} from '@postqron/compliance'
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
} from 'vue'
import {
  defineCatalogs,
  localizeUrl,
  type Locale,
} from '../../f36-i18n/src/index.ts'
import { usePostqronI18n } from '../../f36-i18n/runtime.ts'

const STORAGE_KEY = 'postqron.cookie-choice'
const CACHE_SCHEMA = 1
const CONSENT_EVENT = 'postqron:cookie-preferences-changed'
const optionalCategories = COOKIE_CATEGORIES.filter(
  (category): category is OptionalCookieCategory => category !== 'necessary',
)

const COOKIE_CATALOGS = defineCatalogs({
  en: {
    eyebrow: 'Your choice',
    title: 'Cookies under your control',
    description: 'We always use necessary cookies only. Optional technologies stay off until you make a valid choice.',
    policyLink: 'Read the Cookie Policy',
    necessaryTitle: 'Necessary',
    necessaryDescription: 'Required for the site to work and cannot be switched off.',
    alwaysActive: 'Always active',
    preferencesTitle: 'Preferences',
    preferencesDescription: 'Remember settings you choose to make the site more convenient.',
    analyticsTitle: 'Analytics',
    analyticsDescription: 'Help us understand, in aggregate, how the site is used.',
    marketingTitle: 'Marketing',
    marketingDescription: 'Measure campaigns and promotional content.',
    acceptAll: 'Accept all',
    rejectAll: 'Reject all',
    customize: 'Customize',
    saveCustom: 'Save preferences',
    close: 'Close preferences',
    loading: 'Checking your saved cookie choice…',
    saving: 'Saving your cookie choice…',
    retry: 'Try again',
    loadError: 'We could not verify your saved choice. Optional technologies remain off. Try again.',
    saveError: 'We could not save your choice. No new optional technology was enabled. Try again.',
    policyChanged: 'The Cookie Policy changed while you were choosing. Review the options and try again.',
    gpcNotice: 'Global Privacy Control is active. Marketing technologies will remain off.',
  },
  it: {
    eyebrow: 'La tua scelta',
    title: 'Cookie sotto il tuo controllo',
    description: 'Usiamo sempre solo i cookie necessari. Le tecnologie opzionali restano disattivate finché non esprimi una scelta valida.',
    policyLink: 'Leggi la Cookie Policy',
    necessaryTitle: 'Necessari',
    necessaryDescription: 'Servono al funzionamento del sito e non possono essere disattivati.',
    alwaysActive: 'Sempre attivi',
    preferencesTitle: 'Preferenze',
    preferencesDescription: 'Ricordano impostazioni che scegli per rendere il sito più comodo.',
    analyticsTitle: 'Analisi',
    analyticsDescription: 'Aiutano a capire in forma aggregata come viene usato il sito.',
    marketingTitle: 'Marketing',
    marketingDescription: 'Misurano campagne e contenuti promozionali.',
    acceptAll: 'Accetta tutto',
    rejectAll: 'Rifiuta tutto',
    customize: 'Personalizza',
    saveCustom: 'Salva preferenze',
    close: 'Chiudi preferenze',
    loading: 'Verifica della scelta cookie salvata…',
    saving: 'Salvataggio della scelta cookie…',
    retry: 'Riprova',
    loadError: 'Non è stato possibile verificare la scelta salvata. Le tecnologie opzionali restano disattivate. Riprova.',
    saveError: 'Non è stato possibile salvare la scelta. Nessuna nuova tecnologia opzionale è stata attivata. Riprova.',
    policyChanged: 'La Cookie Policy è cambiata durante la scelta. Controlla le opzioni e riprova.',
    gpcNotice: 'Global Privacy Control è attivo. Le tecnologie di marketing resteranno disattivate.',
  },
  es: {
    eyebrow: 'Tu elección',
    title: 'Cookies bajo tu control',
    description: 'Usamos siempre solo las cookies necesarias. Las tecnologías opcionales permanecen desactivadas hasta que hagas una elección válida.',
    policyLink: 'Leer la Política de Cookies',
    necessaryTitle: 'Necesarias',
    necessaryDescription: 'Son imprescindibles para que el sitio funcione y no se pueden desactivar.',
    alwaysActive: 'Siempre activas',
    preferencesTitle: 'Preferencias',
    preferencesDescription: 'Recuerdan los ajustes que eliges para que el sitio sea más cómodo.',
    analyticsTitle: 'Analítica',
    analyticsDescription: 'Ayudan a comprender de forma agregada cómo se utiliza el sitio.',
    marketingTitle: 'Marketing',
    marketingDescription: 'Miden campañas y contenido promocional.',
    acceptAll: 'Aceptar todo',
    rejectAll: 'Rechazar todo',
    customize: 'Personalizar',
    saveCustom: 'Guardar preferencias',
    close: 'Cerrar preferencias',
    loading: 'Comprobando tu elección de cookies guardada…',
    saving: 'Guardando tu elección de cookies…',
    retry: 'Reintentar',
    loadError: 'No pudimos verificar tu elección guardada. Las tecnologías opcionales permanecen desactivadas. Inténtalo de nuevo.',
    saveError: 'No pudimos guardar tu elección. No se activó ninguna tecnología opcional nueva. Inténtalo de nuevo.',
    policyChanged: 'La Política de Cookies cambió mientras elegías. Revisa las opciones e inténtalo de nuevo.',
    gpcNotice: 'Global Privacy Control está activo. Las tecnologías de marketing permanecerán desactivadas.',
  },
  fr: {
    eyebrow: 'Votre choix',
    title: 'Les cookies sous votre contrôle',
    description: 'Nous utilisons toujours uniquement les cookies nécessaires. Les technologies facultatives restent désactivées jusqu’à votre choix valide.',
    policyLink: 'Lire la Politique relative aux cookies',
    necessaryTitle: 'Nécessaires',
    necessaryDescription: 'Indispensables au fonctionnement du site, ils ne peuvent pas être désactivés.',
    alwaysActive: 'Toujours actifs',
    preferencesTitle: 'Préférences',
    preferencesDescription: 'Mémorisent vos réglages pour rendre le site plus pratique.',
    analyticsTitle: 'Analyse',
    analyticsDescription: 'Aident à comprendre de manière agrégée comment le site est utilisé.',
    marketingTitle: 'Marketing',
    marketingDescription: 'Mesurent les campagnes et les contenus promotionnels.',
    acceptAll: 'Tout accepter',
    rejectAll: 'Tout refuser',
    customize: 'Personnaliser',
    saveCustom: 'Enregistrer les préférences',
    close: 'Fermer les préférences',
    loading: 'Vérification de votre choix de cookies enregistré…',
    saving: 'Enregistrement de votre choix de cookies…',
    retry: 'Réessayer',
    loadError: 'Impossible de vérifier votre choix enregistré. Les technologies facultatives restent désactivées. Réessayez.',
    saveError: 'Impossible d’enregistrer votre choix. Aucune nouvelle technologie facultative n’a été activée. Réessayez.',
    policyChanged: 'La Politique relative aux cookies a changé pendant votre choix. Vérifiez les options et réessayez.',
    gpcNotice: 'Global Privacy Control est actif. Les technologies marketing resteront désactivées.',
  },
  de: {
    eyebrow: 'Ihre Auswahl',
    title: 'Cookies unter Ihrer Kontrolle',
    description: 'Wir verwenden immer nur notwendige Cookies. Optionale Technologien bleiben aus, bis Sie eine gültige Auswahl treffen.',
    policyLink: 'Cookie-Richtlinie lesen',
    necessaryTitle: 'Notwendig',
    necessaryDescription: 'Für den Betrieb der Website erforderlich und nicht deaktivierbar.',
    alwaysActive: 'Immer aktiv',
    preferencesTitle: 'Präferenzen',
    preferencesDescription: 'Speichern Ihre Einstellungen, um die Website komfortabler zu machen.',
    analyticsTitle: 'Analyse',
    analyticsDescription: 'Helfen uns in zusammengefasster Form zu verstehen, wie die Website genutzt wird.',
    marketingTitle: 'Marketing',
    marketingDescription: 'Messen Kampagnen und Werbeinhalte.',
    acceptAll: 'Alle akzeptieren',
    rejectAll: 'Alle ablehnen',
    customize: 'Anpassen',
    saveCustom: 'Einstellungen speichern',
    close: 'Einstellungen schließen',
    loading: 'Ihre gespeicherte Cookie-Auswahl wird geprüft…',
    saving: 'Ihre Cookie-Auswahl wird gespeichert…',
    retry: 'Erneut versuchen',
    loadError: 'Ihre gespeicherte Auswahl konnte nicht geprüft werden. Optionale Technologien bleiben deaktiviert. Versuchen Sie es erneut.',
    saveError: 'Ihre Auswahl konnte nicht gespeichert werden. Es wurde keine neue optionale Technologie aktiviert. Versuchen Sie es erneut.',
    policyChanged: 'Die Cookie-Richtlinie wurde während Ihrer Auswahl geändert. Prüfen Sie die Optionen und versuchen Sie es erneut.',
    gpcNotice: 'Global Privacy Control ist aktiv. Marketing-Technologien bleiben deaktiviert.',
  },
})

type CookieMessageKey = keyof typeof COOKIE_CATALOGS.en
type CookieSelection = Record<OptionalCookieCategory, boolean>
type CookieSource = 'banner' | 'preferences_center'

interface ApiCookiePreferences extends CookieSelection {
  necessary: true
  has_recorded_choice: boolean
  policy_version: string
  policy_digest_sha256: string
  selected_at: string | null
  expires_at: string | null
  source?: CookieSource | 'account'
  revision: number
}

interface CacheEnvelope {
  schema: typeof CACHE_SCHEMA
  state: ApiCookiePreferences
}

interface PendingSave {
  key: string
  selection: CookieSelection
  source: CookieSource
}

interface CookieConsentGate {
  allows(category: OptionalCookieCategory): boolean
}

const registeredRuntimes = new WeakSet<object>()
const i18n = usePostqronI18n()
if (!registeredRuntimes.has(i18n)) {
  i18n.registerCatalog('cookiePreferences', COOKIE_CATALOGS)
  registeredRuntimes.add(i18n)
}

const requestFetch = useRequestFetch()
const panel = ref<HTMLElement>()
const show = ref(false)
const customize = ref(false)
const loading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const policyVersion = ref('')
const confirmedChoice = ref(false)
const globalPrivacyControl = ref(false)
const pendingSave = ref<PendingSave>()
const selection = reactive<CookieSelection>(emptySelection())
const activeSelection = reactive<CookieSelection>(emptySelection())
let lastFocused: HTMLElement | null = null

const busy = computed(() => loading.value || saving.value)
const locale = computed(() => i18n.locale.value as Locale)
const policyPath = computed(() => localizeUrl(locale.value, '/legal/cookie'))
const firstLevelActions = computed(() =>
  COOKIE_BANNER_FIRST_LEVEL_ACTIONS.map(action => ({
    ...action,
    label: translate(
      action.id === 'accept_all'
        ? 'acceptAll'
        : action.id === 'reject_all'
          ? 'rejectAll'
          : 'customize',
    ),
  })),
)
const acceptAllLabel = computed(() =>
  firstLevelActions.value.find(action => action.id === 'accept_all')!.label,
)
const rejectAllLabel = computed(() =>
  firstLevelActions.value.find(action => action.id === 'reject_all')!.label,
)
const customizeLabel = computed(() =>
  firstLevelActions.value.find(action => action.id === 'customize')!.label,
)
const categoryLabels = computed(() => ({
  preferences: {
    title: translate('preferencesTitle'),
    description: translate('preferencesDescription'),
  },
  analytics: {
    title: translate('analyticsTitle'),
    description: translate('analyticsDescription'),
  },
  marketing: {
    title: translate('marketingTitle'),
    description: translate('marketingDescription'),
  },
}))

function translate(key: CookieMessageKey): string {
  return i18n.translate(`cookiePreferences.${key}`)
}

function emptySelection(): CookieSelection {
  return {
    preferences: false,
    analytics: false,
    marketing: false,
  }
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('COOKIE_PREFERENCES_INVALID_RESPONSE')
  }
  return value as Record<string, unknown>
}

function validInstant(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value))
}

function normalizePreferences(value: unknown): ApiCookiePreferences {
  const candidate = asRecord(value)
  for (const category of optionalCategories) {
    if (typeof candidate[category] !== 'boolean') {
      throw new Error('COOKIE_PREFERENCES_INVALID_RESPONSE')
    }
  }
  if (
    candidate.necessary !== true
    || typeof candidate.has_recorded_choice !== 'boolean'
    || typeof candidate.policy_version !== 'string'
    || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(candidate.policy_version)
    || typeof candidate.policy_digest_sha256 !== 'string'
    || !/^[a-f0-9]{64}$/u.test(candidate.policy_digest_sha256)
    || !Number.isInteger(candidate.revision)
    || Number(candidate.revision) < 0
  ) {
    throw new Error('COOKIE_PREFERENCES_INVALID_RESPONSE')
  }

  const selectedAt = candidate.selected_at
  const expiresAt = candidate.expires_at
  if (
    (selectedAt !== null && !validInstant(selectedAt))
    || (expiresAt !== null && !validInstant(expiresAt))
    || (
      candidate.has_recorded_choice
      && (
        !validInstant(selectedAt)
        || !validInstant(expiresAt)
        || new Date(expiresAt) <= new Date()
      )
    )
  ) {
    throw new Error('COOKIE_PREFERENCES_INVALID_RESPONSE')
  }

  return {
    necessary: true,
    preferences: candidate.preferences as boolean,
    analytics: candidate.analytics as boolean,
    marketing: candidate.marketing as boolean,
    has_recorded_choice: candidate.has_recorded_choice,
    policy_version: candidate.policy_version,
    policy_digest_sha256: candidate.policy_digest_sha256,
    selected_at: selectedAt as string | null,
    expires_at: expiresAt as string | null,
    source: candidate.source as ApiCookiePreferences['source'],
    revision: candidate.revision as number,
  }
}

function readCache(): ApiCookiePreferences | undefined {
  try {
    const raw = globalThis.localStorage.getItem(STORAGE_KEY)
    if (!raw) {
      return
    }
    const cached = asRecord(JSON.parse(raw))
    if (cached.schema !== CACHE_SCHEMA) {
      return
    }
    return normalizePreferences(cached.state)
  } catch {
    globalThis.localStorage.removeItem(STORAGE_KEY)
    return
  }
}

function writeCache(state: ApiCookiePreferences): void {
  const envelope: CacheEnvelope = { schema: CACHE_SCHEMA, state }
  globalThis.localStorage.setItem(STORAGE_KEY, JSON.stringify(envelope))
}

function expireClientCookie(name: string): void {
  const safeName = name.trim()
  if (!/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/u.test(safeName)) {
    return
  }
  const secure = globalThis.location.protocol === 'https:' ? '; Secure' : ''
  globalThis.document.cookie = `${safeName}=; Max-Age=0; Path=/; SameSite=Lax${secure}`
}

function revokeManagedTechnology(category: OptionalCookieCategory): void {
  const selector = `[data-postqron-cookie-category="${category}"]`
  for (const element of globalThis.document.querySelectorAll<HTMLElement>(selector)) {
    for (const name of (element.dataset.postqronCookieNames || '').split(',')) {
      if (name.trim()) {
        expireClientCookie(name)
      }
    }
    if (element.dataset.postqronManaged === 'true') {
      element.remove()
    }
  }
}

function syncOptionalTechnologies(next: CookieSelection): void {
  Object.assign(activeSelection, next)
  for (const category of optionalCategories) {
    globalThis.document.documentElement.dataset[
      `cookie${category[0]!.toUpperCase()}${category.slice(1)}`
    ] = next[category] ? 'granted' : 'denied'
    if (!next[category]) {
      revokeManagedTechnology(category)
    }
  }
  globalThis.window.dispatchEvent(new globalThis.CustomEvent(CONSENT_EVENT, {
    detail: Object.freeze({ ...next }),
  }))
}

function installConsentGate(): void {
  const browserWindow = globalThis.window as Window & {
    postqronCookieConsent?: CookieConsentGate
  }
  browserWindow.postqronCookieConsent = Object.freeze({
    allows: (category: OptionalCookieCategory) => activeSelection[category],
  })
}

function privacySafeSelection(next: CookieSelection): CookieSelection {
  return {
    ...next,
    marketing: globalPrivacyControl.value ? false : next.marketing,
  }
}

function revokeBeforePersistence(next: CookieSelection): void {
  syncOptionalTechnologies({
    preferences: activeSelection.preferences && next.preferences,
    analytics: activeSelection.analytics && next.analytics,
    marketing: activeSelection.marketing && next.marketing,
  })
}

function applyServerState(state: ApiCookiePreferences): void {
  policyVersion.value = state.policy_version
  confirmedChoice.value = state.has_recorded_choice
  const serverSelection = privacySafeSelection({
    preferences: state.preferences,
    analytics: state.analytics,
    marketing: state.marketing,
  })
  Object.assign(selection, serverSelection)
  syncOptionalTechnologies(
    state.has_recorded_choice ? serverSelection : emptySelection(),
  )
}

async function loadFromServer(forceOpen = false): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  pendingSave.value = undefined
  try {
    const state = normalizePreferences(
      await requestFetch<unknown>('/api/cookie-preferences', {
        headers: { accept: 'application/json' },
      }),
    )
    writeCache(state)
    applyServerState(state)
    show.value = forceOpen || !state.has_recorded_choice
    if (!state.has_recorded_choice && !forceOpen) {
      customize.value = false
    }
  } catch {
    confirmedChoice.value = false
    policyVersion.value = ''
    syncOptionalTechnologies(emptySelection())
    show.value = true
    errorMessage.value = translate('loadError')
  } finally {
    loading.value = false
  }
}

async function persistChoice(
  requested: CookieSelection,
  source: CookieSource,
  key: string = globalThis.crypto.randomUUID(),
): Promise<void> {
  if (!policyVersion.value) {
    await loadFromServer(true)
    return
  }
  const next = privacySafeSelection(requested)
  const retry: PendingSave = { key, selection: next, source }
  pendingSave.value = retry
  saving.value = true
  errorMessage.value = ''
  revokeBeforePersistence(next)
  try {
    const state = normalizePreferences(
      await requestFetch<unknown>('/api/cookie-preferences', {
        method: 'PUT',
        headers: {
          'Idempotency-Key': key,
        },
        body: {
          policy_version: policyVersion.value,
          source,
          ...next,
        },
      }),
    )
    writeCache(state)
    applyServerState(state)
    pendingSave.value = undefined
    show.value = false
    customize.value = false
    restoreFocus()
  } catch (error) {
    const status = (error as { status?: number, statusCode?: number }).statusCode
      ?? (error as { status?: number }).status
    if (status === 409) {
      pendingSave.value = undefined
      await loadFromServer(true)
      errorMessage.value = translate('policyChanged')
    } else {
      errorMessage.value = translate('saveError')
    }
  } finally {
    saving.value = false
  }
}

function acceptAll(): Promise<void> {
  return persistChoice(
    { preferences: true, analytics: true, marketing: true },
    customize.value ? 'preferences_center' : 'banner',
  )
}

function rejectAll(): Promise<void> {
  return persistChoice(
    emptySelection(),
    customize.value ? 'preferences_center' : 'banner',
  )
}

function saveCustom(): Promise<void> {
  return persistChoice({ ...selection }, 'preferences_center')
}

function retry(): Promise<void> {
  const pending = pendingSave.value
  return pending
    ? persistChoice(pending.selection, pending.source, pending.key)
    : loadFromServer(customize.value)
}

function rememberFocus(): void {
  if (globalThis.document.activeElement instanceof HTMLElement) {
    lastFocused = globalThis.document.activeElement
  }
}

async function focusPanel(): Promise<void> {
  await nextTick()
  const first = focusableElements()[0]
  ;(first ?? panel.value)?.focus()
}

function openCustomization(): void {
  rememberFocus()
  customize.value = true
  void focusPanel()
}

function openPreferences(): void {
  rememberFocus()
  show.value = true
  customize.value = true
  void focusPanel()
  void loadFromServer(true)
}

function restoreFocus(): void {
  lastFocused?.focus()
  lastFocused = null
}

function closePreferences(): void {
  if (!confirmedChoice.value) {
    return
  }
  show.value = false
  customize.value = false
  errorMessage.value = ''
  restoreFocus()
}

function focusableElements(): HTMLElement[] {
  if (!panel.value) {
    return []
  }
  return Array.from(panel.value.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )).filter(element => !element.hidden)
}

function trapFocus(event: KeyboardEvent): void {
  if (event.key === 'Escape' && confirmedChoice.value) {
    event.preventDefault()
    closePreferences()
    return
  }
  if (event.key !== 'Tab' || !customize.value) {
    return
  }
  const focusable = focusableElements()
  if (!focusable.length) {
    event.preventDefault()
    panel.value?.focus()
    return
  }
  const first = focusable[0]!
  const last = focusable[focusable.length - 1]!
  if (event.shiftKey && globalThis.document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && globalThis.document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

function reconcileFromOtherTab(event: StorageEvent): void {
  if (event.key === STORAGE_KEY) {
    void loadFromServer(customize.value)
  }
}

onMounted(() => {
  installConsentGate()
  syncOptionalTechnologies(emptySelection())
  globalPrivacyControl.value = (
    globalThis.navigator as Navigator & { globalPrivacyControl?: boolean }
  ).globalPrivacyControl === true
  const cached = readCache()
  if (cached) {
    Object.assign(selection, privacySafeSelection(cached))
  }
  show.value = !cached?.has_recorded_choice
  globalThis.window.addEventListener('postqron:open-cookie-preferences', openPreferences)
  globalThis.window.addEventListener('storage', reconcileFromOtherTab)
  void loadFromServer()
})

onBeforeUnmount(() => {
  globalThis.window.removeEventListener('postqron:open-cookie-preferences', openPreferences)
  globalThis.window.removeEventListener('storage', reconcileFromOtherTab)
})
</script>

<template>
  <aside
    v-if="show"
    ref="panel"
    class="cookie-panel"
    :role="customize ? 'dialog' : 'region'"
    :aria-modal="customize ? 'true' : undefined"
    aria-labelledby="cookie-title"
    aria-describedby="cookie-description"
    tabindex="-1"
    @keydown="trapFocus"
  >
    <div class="cookie-panel__copy">
      <button
        v-if="customize && confirmedChoice"
        class="cookie-panel__close"
        type="button"
        :aria-label="translate('close')"
        :disabled="busy"
        @click="closePreferences"
      >
        <span aria-hidden="true">×</span>
      </button>
      <p class="eyebrow">
        {{ translate('eyebrow') }}
      </p>
      <h2 id="cookie-title">
        {{ translate('title') }}
      </h2>
      <p id="cookie-description">
        {{ translate('description') }}
      </p>
      <NuxtLink :to="policyPath">
        {{ translate('policyLink') }}
      </NuxtLink>
      <p
        v-if="globalPrivacyControl"
        class="cookie-panel__notice"
        role="status"
      >
        {{ translate('gpcNotice') }}
      </p>
    </div>

    <div
      v-if="customize"
      class="cookie-panel__options"
    >
      <div class="cookie-option">
        <span>
          <strong>{{ translate('necessaryTitle') }}</strong>
          <small>{{ translate('necessaryDescription') }}</small>
        </span>
        <span class="cookie-option__status">{{ translate('alwaysActive') }}</span>
      </div>
      <label
        v-for="category in optionalCategories"
        :key="category"
        class="cookie-option"
        :for="`cookie-category-${category}`"
      >
        <span>
          <strong>{{ categoryLabels[category].title }}</strong>
          <small :id="`cookie-category-${category}-description`">
            {{ categoryLabels[category].description }}
          </small>
        </span>
        <input
          :id="`cookie-category-${category}`"
          v-model="selection[category]"
          type="checkbox"
          :name="category"
          :aria-describedby="`cookie-category-${category}-description`"
          :disabled="busy || (category === 'marketing' && globalPrivacyControl)"
        >
      </label>
    </div>

    <p
      v-if="loading || saving"
      class="cookie-panel__status"
      role="status"
      aria-live="polite"
    >
      {{ translate(saving ? 'saving' : 'loading') }}
    </p>

    <div
      v-if="errorMessage"
      class="cookie-panel__error"
      role="alert"
    >
      <p>{{ errorMessage }}</p>
      <button
        class="cookie-action"
        type="button"
        :disabled="busy"
        @click="retry"
      >
        {{ translate('retry') }}
      </button>
    </div>

    <div class="cookie-panel__actions">
      <button
        class="cookie-action"
        type="button"
        :disabled="busy || !policyVersion"
        @click="acceptAll"
      >
        {{ acceptAllLabel }}
      </button>
      <button
        class="cookie-action"
        type="button"
        :disabled="busy || !policyVersion"
        @click="rejectAll"
      >
        {{ rejectAllLabel }}
      </button>
      <button
        v-if="!customize"
        class="cookie-action"
        type="button"
        :disabled="busy || !policyVersion"
        @click="openCustomization"
      >
        {{ customizeLabel }}
      </button>
      <button
        v-else
        class="cookie-action"
        type="button"
        :disabled="busy || !policyVersion"
        @click="saveCustom"
      >
        {{ translate('saveCustom') }}
      </button>
    </div>
  </aside>
</template>

<style scoped>
.cookie-panel:focus-visible,
.cookie-action:focus-visible,
.cookie-panel__close:focus-visible,
.cookie-option input:focus-visible,
.cookie-panel a:focus-visible {
  outline: 3px solid var(--pq-color-accent);
  outline-offset: 3px;
}

.cookie-panel__copy {
  position: relative;
}

.cookie-panel__close {
  position: absolute;
  top: 0;
  right: 0;
  display: grid;
  width: var(--pq-size-target-min);
  height: var(--pq-size-target-min);
  place-items: center;
  border: 2px solid var(--pq-color-brand);
  border-radius: var(--pq-radius-md);
  color: var(--pq-color-brand);
  background: var(--pq-color-surface);
  font: inherit;
  font-size: var(--pq-font-size-xl);
  cursor: pointer;
}

.cookie-panel__notice,
.cookie-panel__status {
  color: var(--pq-color-text);
  font-weight: var(--pq-font-weight-semibold);
}

.cookie-panel__error {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pq-space-3);
  align-items: center;
}

.cookie-panel__error p {
  margin: 0;
}

.cookie-panel__error .cookie-action {
  flex: 0 0 auto;
}
</style>
