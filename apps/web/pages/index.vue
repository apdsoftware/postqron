<script setup lang="ts">
import { DEFAULT_LOCALE, LOCALES, detectLocale, localePath } from '~/utils/locale'
import { alternateLinks, canonicalUrl } from '~/utils/site'

/**
 * Radice: smistamento, non contenuto.
 *
 * Il sito è statico e nessun server legge `Accept-Language` (SPEC §2), quindi
 * il rilevamento avviene qui, nel browser. Questa pagina **non ha contenuto
 * proprio**: se ne avesse, diventerebbe una sesta versione della home da
 * tradurre e tenere allineata alle altre cinque.
 *
 * Per lo stesso motivo non ha layout: intestazione e piè di pagina sono in una
 * lingua, e qui la lingua non è ancora stata decisa.
 */
definePageMeta({ layout: false })

const { public: config } = useRuntimeConfig()

/**
 * Ordine di precedenza (R31, R32): la scelta esplicita fatta col selettore, poi
 * le preferenze del browser, infine l'inglese. Il primo termine è ciò che rende
 * il selettore una scelta e non un salto: chi ha scelto lo spagnolo torna sullo
 * spagnolo anche arrivando dalla radice, qualunque cosa dica il browser.
 */
onMounted(() => {
  const locale = rememberedLocale() ?? detectLocale(navigator.languages)

  void navigateTo(localePath('/', locale), { replace: true })
})

/**
 * Senza JavaScript il reindirizzamento non avviene: restano questi cinque link,
 * che sono anche la strada con cui il crawler del pre-rendering raggiunge tutte
 * le lingue. Non c'è nulla da tradurre — ogni voce è scritta nella propria.
 *
 * Non è markup del template perché il contenuto di `<noscript>` è testo per il
 * browser quando JavaScript è attivo: farlo idratare a Vue significherebbe far
 * combaciare un albero di elementi con una stringa. Qui invece esce dalla build
 * già serializzato, e nessuno prova a riconoscerlo.
 */
const languageList = LOCALES
  .map(entry =>
    `<li><a href="${localePath('/', entry.code)}" hreflang="${entry.htmlLang}" `
    + `lang="${entry.htmlLang}">${entry.label}</a></li>`,
  )
  .join('')

useHead({
  htmlAttrs: { lang: DEFAULT_LOCALE },
  link: [
    { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
    // La radice non va indicizzata al posto di una delle traduzioni: si
    // dichiara canonica la home inglese, che è anche la destinazione di
    // `x-default`.
    { rel: 'canonical', href: canonicalUrl(localePath('/', DEFAULT_LOCALE), config.siteUrl) },
    ...alternateLinks('/', config.siteUrl).map(alternate => ({
      rel: 'alternate',
      hreflang: alternate.hreflang,
      href: alternate.href,
    })),
  ],
  noscript: [{ innerHTML: `<ul>${languageList}</ul>`, tagPosition: 'bodyClose' }],
})
</script>

<template>
  <div class="switchboard" />
</template>

<style scoped>
/*
 * La pagina non disegna nulla: tiene solo il colore di sfondo per l'istante fra
 * il primo disegno e il reindirizzamento, che altrimenti sarebbe un lampo
 * bianco fra una pagina scura e l'altra.
 */
.switchboard {
  min-height: 100vh;
  background: var(--pq-page);
}
</style>
