// Sito pubblico postqron.com.
//
// Vincolo di distribuzione (docs/SPEC.md §2): l'output deve essere interamente
// statico e servibile da Cloudflare Pages. Qui l'SSR è attivo solo in fase di
// build — `nuxt generate` pre-renderizza tutte le rotte per la SEO e non
// resta alcun server Nitro in produzione. Non introdurre `server/` API routes,
// `defineEventHandler` o rotte non pre-renderizzabili: l'unica origin dinamica
// è il backend Go.
import { LOCALE_CODES, localePath } from './utils/locale'

export default defineNuxtConfig({
  modules: ['@nuxt/eslint'],

  // Pre-rendering in build, nessun rendering a runtime.
  ssr: true,

  // I componenti sono raggruppati per ruolo (ui, layout, home) ma il nome resta
  // quello del file: `<FeatureCard>`, non `<UiFeatureCard>`. I nomi sono già
  // univoci e il prefisso della cartella non aggiungerebbe informazione.
  components: [{ path: '~/components', pathPrefix: false }],

  devtools: { enabled: true },

  // I fogli globali del design system, nell'ordine in cui devono cascare:
  // token, poi @font-face, poi reset e primitive di layout.
  css: [
    '~/assets/css/tokens.css',
    '~/assets/css/fonts.css',
    '~/assets/css/base.css',
    '~/assets/css/layout.css',
  ],

  // I valori pubblici vengono incorporati nel bundle al momento della build:
  // su hosting statico non esiste un processo che li possa leggere a runtime.
  // Vanno quindi impostati nell'ambiente di build (NUXT_PUBLIC_*).
  runtimeConfig: {
    public: {
      apiBaseUrl: 'http://localhost:8080',
      siteUrl: 'http://localhost:3000',
    },
  },

  compatibilityDate: '2026-08-17',

  nitro: {
    prerender: {
      crawlLinks: true,

      // Il numero di rotte generate si moltiplica per cinque (SPEC §8-bis). La
      // radice non basta come punto di partenza: è una pagina di smistamento
      // che reindirizza da JavaScript, e il crawler non esegue JavaScript. Le
      // cinque home sono quindi dichiarate qui, e da lì il crawler raggiunge
      // tutto il resto seguendo i link — che, essendo prefissati per lingua,
      // restano dentro la lingua da cui partono.
      routes: ['/', ...LOCALE_CODES.map(locale => localePath('/', locale))],

      // Una rotta che non si pre-renderizza è un errore di build, non un
      // fallback silenzioso su SSR.
      failOnError: true,
    },
  },

  eslint: {
    config: {
      stylistic: true,
    },
  },
})
