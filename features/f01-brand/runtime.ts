export const brandFeature = {
  id: 'brand',
  name: 'Postqron',
  version: '0.1.0',
  assets: {
    favicon: './assets/favicon.svg',
    logo: './assets/logo-primary.svg',
    logoReversed: './assets/logo-reversed.svg',
    logoMonochrome: './assets/logo-monochrome.svg',
    mark: './assets/mark.svg',
    socialCard: './assets/social-card.svg',
  },
  styles: {
    tokens: './tokens/tokens.css',
    components: './components/components.css',
  },
} as const

export type BrandFeature = typeof brandFeature
