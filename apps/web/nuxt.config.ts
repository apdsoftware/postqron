import { delimiter } from 'node:path'
import { fileURLToPath } from 'node:url'

const fromRepository = (path: string) => fileURLToPath(new URL(path, import.meta.url))
const defaultFeatureRoots = [
  fromRepository('./features'),
  fromRepository('../../services/api/features'),
  fromRepository('../../features'),
].join(delimiter)

export default defineNuxtConfig({
  compatibilityDate: '2026-07-24',
  srcDir: fromRepository('../../features/f02-marketing-site'),
  ssr: true,
  devtools: { enabled: false },
  css: [
    fromRepository('../../features/f01-brand/components/components.css'),
    fromRepository('../../features/f02-marketing-site/assets/css/marketing.css'),
  ],
  alias: {
    '@postqron/compliance': fromRepository('../../features/f13-compliance/src/index.ts'),
  },
  plugins: [
    fromRepository('./plugins/pwa.client.ts'),
  ],
  devServer: {
    port: Number(process.env.NUXT_PORT || 3000),
  },
  nitro: {
    preset: 'node-server',
    scanDirs: [
      fromRepository('./server'),
      fromRepository('../../features/f02-marketing-site/server'),
    ],
    publicAssets: [
      {
        dir: fromRepository('../../features/f01-brand/assets'),
        baseURL: '/brand',
        maxAge: 60 * 60 * 24 * 30,
      },
      {
        dir: fromRepository('../../features/f23-pwa/web'),
        baseURL: '/pwa',
        maxAge: 60 * 60 * 24 * 30,
      },
    ],
  },
  app: {
    head: {
      link: [
        {
          rel: 'icon',
          href: '/brand/favicon.svg',
          type: 'image/svg+xml',
        },
        {
          rel: 'manifest',
          href: '/manifest.webmanifest',
        },
      ],
      htmlAttrs: { lang: 'it' },
      meta: [
        { name: 'theme-color', content: '#185c43' },
      ],
    },
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
    apiBase: process.env.POSTQRON_API_BASE
      || process.env.NUXT_PUBLIC_API_BASE
      || 'http://localhost:8080',
    featureRoots: process.env.POSTQRON_FEATURE_ROOTS || defaultFeatureRoots,
    public: {
      apiBase: process.env.NUXT_PUBLIC_API_BASE || 'http://localhost:8080',
      siteUrl: process.env.NUXT_PUBLIC_SITE_URL || 'http://localhost:3000',
      appUrl: process.env.NUXT_PUBLIC_APP_URL || '/app',
      apdSoftwareUrl: process.env.NUXT_PUBLIC_APDSOFTWARE_URL
        || 'https://apdsoftware.it',
    },
  },
  typescript: {
    strict: true,
    typeCheck: true,
  },
})
