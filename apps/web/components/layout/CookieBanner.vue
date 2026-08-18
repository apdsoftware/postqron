<script setup lang="ts">
import { createCookieConsent, currentCookieConsent, persistCookieConsent } from '~/utils/cookieConsent'

const { content, href, locale } = useSiteLocale()
const visible = ref(false)
const banner = ref<HTMLElement | null>(null)

/** Dove riportare il fuoco alla chiusura: il link del piè di pagina che ha aperto. */
let focusOnClose: HTMLElement | null = null

function decide(nonEssential: boolean) {
  persistCookieConsent(createCookieConsent(nonEssential, locale.value))
  close()
}

/**
 * Escape chiude il banner. Se una scelta non era ancora stata fatta, chiudere
 * *è* rifiutare: è l'esito che non attiva nulla, e lasciarlo indeterminato
 * significherebbe riproporre la domanda a ogni pagina. Nella direzione opposta
 * non esiste una scorciatoia — nessun tasto accetta — perché la §3 chiede che
 * rifiutare sia facile almeno quanto accettare, non il contrario.
 *
 * Se invece una scelta c'era già — il banner riaperto dal piè di pagina —
 * Escape la lascia esattamente com'è.
 */
function dismiss() {
  if (currentCookieConsent() === null) {
    persistCookieConsent(createCookieConsent(false, locale.value))
  }

  close()
}

function close() {
  visible.value = false

  const target = focusOnClose
  focusOnClose = null
  if (target?.isConnected) target.focus()
}

function onKeydown(event: KeyboardEvent) {
  if (visible.value && event.key === 'Escape') dismiss()
}

/**
 * Riaperto da un gesto deliberato: qui il fuoco si sposta dentro, perché è
 * quello che l'utente ha appena chiesto. Alla prima visita non si sposta —
 * il banner è il primo elemento del documento e lo si raggiunge al primo Tab.
 */
function openPreferences() {
  focusOnClose = document.activeElement instanceof HTMLElement ? document.activeElement : null
  visible.value = true
  nextTick(() => banner.value?.focus())
}

onMounted(() => {
  visible.value = currentCookieConsent() === null
  window.addEventListener('postqron:cookie-preferences', openPreferences)
  window.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  window.removeEventListener('postqron:cookie-preferences', openPreferences)
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <Transition name="cookie-banner">
    <!--
      `aria-modal="false"` non è ridondante: dice che il banner non chiude fuori
      il resto della pagina, e infatti non lo fa. Non c'è trappola per il fuoco,
      niente `inert` sul documento e nessun ordine di tabulazione forzato — si
      entra e si esce con Tab, e con Escape si chiude.
    -->
    <section
      v-if="visible"
      ref="banner"
      class="cookie-banner"
      role="dialog"
      aria-modal="false"
      tabindex="-1"
      :aria-labelledby="`cookie-banner-title-${locale}`"
      :aria-describedby="`cookie-banner-description-${locale}`"
    >
      <div class="cookie-banner__content">
        <h2 :id="`cookie-banner-title-${locale}`">
          {{ content.cookieBanner.title }}
        </h2>
        <p :id="`cookie-banner-description-${locale}`">
          {{ content.cookieBanner.description }}
          <NuxtLink :to="href('/legal/cookie-policy')">
            {{ content.cookieBanner.policyLink }}
          </NuxtLink>
        </p>
      </div>
      <div class="cookie-banner__choices">
        <button
          class="cookie-banner__choice"
          type="button"
          @click="decide(false)"
        >
          {{ content.cookieBanner.reject }}
        </button>
        <button
          class="cookie-banner__choice"
          type="button"
          @click="decide(true)"
        >
          {{ content.cookieBanner.accept }}
        </button>
      </div>
    </section>
  </Transition>
</template>

<style scoped>
.cookie-banner {
  position: fixed;
  z-index: 1000;
  right: var(--pq-space-4);
  bottom: var(--pq-space-4);
  left: var(--pq-space-4);
  display: flex;
  max-width: 960px;
  margin: 0 auto;
  padding: var(--pq-space-5);
  border: 1px solid var(--pq-border-input);
  border-radius: var(--pq-radius-lg);
  background: var(--pq-surface);
  box-shadow: var(--pq-shadow-card);
  gap: var(--pq-space-5);
}

/* Il contenitore riceve il fuoco solo da programma: un contorno intorno
   all'intero pannello segnalerebbe una navigazione che non è avvenuta. */
.cookie-banner:focus { outline: none; }
.cookie-banner__content { flex: 1; }
.cookie-banner h2 { margin-bottom: var(--pq-space-2); color: var(--pq-heading); font-size: var(--pq-text-xl); }
.cookie-banner p { color: var(--pq-text); font-size: var(--pq-text-xs); line-height: var(--pq-leading-normal); }
.cookie-banner a { color: var(--pq-primary); text-decoration: underline; text-underline-offset: 0.2em; }
.cookie-banner__choices { display: grid; min-width: 280px; grid-template-columns: repeat(2, 1fr); gap: var(--pq-space-3); align-content: center; }
.cookie-banner__choice { min-height: 48px; padding: var(--pq-space-2) var(--pq-space-3); border: 2px solid var(--pq-primary); border-radius: var(--pq-radius-pill); background: var(--pq-surface); color: var(--pq-primary); cursor: pointer; font-weight: var(--pq-weight-semibold); }
.cookie-banner__choice:hover { background: var(--pq-primary); color: var(--pq-text-inverted); }
.cookie-banner__choice:focus-visible, .cookie-banner a:focus-visible { outline: none; box-shadow: var(--pq-ring-strong); }
.cookie-banner-enter-active, .cookie-banner-leave-active { transition: opacity 0.2s ease, transform 0.2s ease; }
.cookie-banner-enter-from, .cookie-banner-leave-to { opacity: 0; transform: translateY(var(--pq-space-2)); }

@media (max-width: 767px) {
  .cookie-banner { flex-direction: column; padding: var(--pq-space-4); }
  .cookie-banner__choices { min-width: 0; }
}
</style>
