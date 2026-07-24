import { fileURLToPath } from 'node:url'

const fromFeature = (path: string) => fileURLToPath(new URL(path, import.meta.url))

export default defineNuxtConfig({
  compatibilityDate: '2026-07-24',
  ssr: true,
  devtools: { enabled: false },
  css: [
    fromFeature('../f01-brand/components/components.css'),
    '~/assets/css/marketing.css',
  ],
  alias: {
    '@postqron/compliance': fromFeature('../f13-compliance/src/index.ts'),
  },
  app: {
    head: {
      htmlAttrs: { lang: 'it' },
      link: [
        { rel: 'icon', href: '/brand/favicon.svg', type: 'image/svg+xml' },
      ],
      meta: [
        { name: 'theme-color', content: '#185c43' },
      ],
    },
  },
  nitro: {
    preset: 'node-server',
    publicAssets: [
      {
        dir: fromFeature('../f01-brand/assets'),
        baseURL: '/brand',
        maxAge: 60 * 60 * 24 * 30,
      },
    ],
  },
  routeRules: {
    '/**': {
      headers: {
        'content-security-policy': "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'",
        'referrer-policy': 'strict-origin-when-cross-origin',
        'x-content-type-options': 'nosniff',
        'x-frame-options': 'DENY',
      },
    },
  },
  runtimeConfig: {
    apiBase: process.env.POSTQRON_API_BASE || 'http://localhost:8080',
    public: {
      siteUrl: process.env.NUXT_PUBLIC_SITE_URL || 'http://localhost:3000',
      appUrl: process.env.NUXT_PUBLIC_APP_URL || '/app',
      apdSoftwareUrl: process.env.NUXT_PUBLIC_APDSOFTWARE_URL || 'https://apdsoftware.it',
    },
  },
  typescript: {
    strict: true,
    typeCheck: true,
  },
})
