// Dashboard cliente e amministratore.
//
// Vincolo di distribuzione (docs/SPEC.md §2): output interamente statico su
// Cloudflare Pages. A differenza del sito pubblico qui l'SSR è disattivato del
// tutto: le pagine sono dietro autenticazione, non hanno valore SEO e i dati
// arrivano dal backend Go via fetch lato client. `nuxt generate` produce quindi
// una SPA statica (vedi public/_redirects per il fallback di routing).
// Non introdurre `server/` API routes: nessun Nitro gira in produzione.
export default defineNuxtConfig({
  modules: ['@nuxt/eslint'],

  ssr: false,

  devtools: { enabled: true },

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

  eslint: {
    config: {
      stylistic: true,
    },
  },
})
