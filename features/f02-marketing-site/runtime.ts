export const marketingSiteFeature = {
  id: 'marketing-site',
  version: '0.1.0',
  rendering: 'ssr',
  locale: 'it-IT',
  routes: [
    '/',
    '/funzionalita',
    '/prezzi',
    '/faq',
    '/legal/termini',
    '/legal/privacy',
    '/legal/cookie',
  ],
  integrations: {
    brand: 'brand',
    publicCatalog: '/api/v1/billing/plans',
    legalDocuments: '/api/v1/legal-documents/{documentKey}/current',
    cookiePreferences: '/api/v1/cookie-preferences',
  },
} as const

export type MarketingSiteFeature = typeof marketingSiteFeature
