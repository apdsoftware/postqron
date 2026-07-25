import { delimiter, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  discoverFeatureComposition,
} from './server/utils/feature-module'
import {
  STATIC_CONTENT_SECURITY_POLICY,
} from './server/utils/content-security-policy'

const fromRepository = (path: string) => fileURLToPath(new URL(path, import.meta.url))
const defaultFeatureRoots = [
  fromRepository('./features'),
  fromRepository('../../services/api/features'),
  fromRepository('../../features'),
].join(delimiter)
const configuredFeatureRoots = process.env.POSTQRON_FEATURE_ROOTS || defaultFeatureRoots
const featureComposition = await discoverFeatureComposition(
  configuredFeatureRoots
    .split(delimiter)
    .map(root => resolve(process.cwd(), root)),
)

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
    ...featureComposition.plugins,
  ],
  hooks: {
    'app:resolve': (app) => {
      for (const layout of featureComposition.layouts) {
        app.layouts[layout.name] = {
          name: layout.name,
          file: layout.file,
        }
      }
      for (const middleware of featureComposition.middleware) {
        app.middleware.push({
          name: middleware.name,
          path: middleware.path,
          global: middleware.global,
        })
      }
    },
    'components:dirs': (directories) => {
      for (const path of featureComposition.components) {
        directories.push({ path })
      }
    },
    'pages:extend': (pages) => {
      for (const route of featureComposition.routes) {
        pages.push({
          name: route.name,
          path: route.path,
          file: route.file,
          middleware: route.middleware,
          meta: {
            featureId: route.featureId,
            featureVisibility: route.visibility,
          },
        })
      }
    },
  },
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
        'content-security-policy': STATIC_CONTENT_SECURITY_POLICY,
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
    featureRoots: configuredFeatureRoots,
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
