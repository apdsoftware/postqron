// Dashboard cliente e amministratore.
//
// Vincolo di distribuzione (docs/SPEC.md §2): output interamente statico su
// Cloudflare Pages. A differenza del sito pubblico qui l'SSR è disattivato del
// tutto: le pagine sono dietro autenticazione, non hanno valore SEO e i dati
// arrivano dal backend Go via fetch lato client. `nuxt generate` produce quindi
// una SPA statica (vedi public/_redirects per il fallback di routing).
// Non introdurre `server/` API routes: nessun Nitro gira in produzione.
import tailwindcss from '@tailwindcss/vite'
import { COLOR_SCHEME_BOOT_SCRIPT } from './utils/color-scheme'

export default defineNuxtConfig({
  modules: ['@nuxt/eslint'],

  ssr: false,

  // I componenti sono raggruppati per ruolo ma il nome resta quello del file:
  // `<AppSidebar>`, non `<LayoutAppSidebar>`. Stessa convenzione di `apps/web`.
  components: [{ path: '~/components', pathPrefix: false }],

  devtools: { enabled: true },

  // Il tema, applicato prima del primo pixel: il perché sta in
  // `utils/color-scheme.ts`, accanto al frammento.
  app: {
    head: {
      script: [{ innerHTML: COLOR_SCHEME_BOOT_SCRIPT, tagPosition: 'head' }],
    },
  },

  // Il tema Tailwind/Flowbite. È l'unico foglio globale: tutto il resto sono
  // utility nel markup, che è il modo in cui il template è scritto.
  css: ['~/assets/css/theme.css'],

  // Incorporati nel bundle al momento della build (NUXT_PUBLIC_*): su hosting
  // statico non c'è alcun processo che possa leggerli a runtime.
  runtimeConfig: {
    public: {
      apiBaseUrl: 'http://localhost:8080',
    },
  },

  compatibilityDate: '2026-08-17',

  nitro: {
    prerender: {
      failOnError: true,
    },
  },

  // Tailwind 4 gira come plugin Vite e non più via PostCSS: è il percorso che il
  // progetto Tailwind indica come predefinito ed è quello che compila anche le
  // classi scritte dentro i `.vue`.
  vite: {
    plugins: [tailwindcss()],
  },

  eslint: {
    config: {
      stylistic: true,
    },
  },
})
