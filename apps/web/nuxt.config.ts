export default defineNuxtConfig({
  compatibilityDate: '2026-07-24',
  css: ['~/assets/css/main.css'],
  devtools: { enabled: false },
  devServer: {
    port: Number(process.env.NUXT_PORT || 3000),
  },
  nitro: {
    preset: 'node-server',
  },
  runtimeConfig: {
    featureRoots: process.env.POSTQRON_FEATURE_ROOTS || 'features',
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE || 'http://localhost:8080',
    },
  },
  typescript: {
    strict: true,
    typeCheck: true,
  },
})
