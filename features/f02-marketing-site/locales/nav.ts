import { defineCatalogs, type CatalogShape } from '../../f36-i18n/src/catalog.ts'

const en = {
  'brand.homeLabel': 'Postqron, home',
  'links.features': 'Features',
  'links.pricing': 'Pricing',
  'links.faq': 'FAQ',
  'cta.start': 'Start now',
  'nav.primaryLabel': 'Main navigation',
  'nav.mobileLabel': 'Mobile navigation',
  'menu.openLabel': 'Open menu',
} as const

const it: CatalogShape<typeof en> = {
  'brand.homeLabel': 'Postqron, home',
  'links.features': 'Funzionalità',
  'links.pricing': 'Prezzi',
  'links.faq': 'FAQ',
  'cta.start': 'Inizia ora',
  'nav.primaryLabel': 'Navigazione principale',
  'nav.mobileLabel': 'Navigazione mobile',
  'menu.openLabel': 'Apri il menu',
}

const es: CatalogShape<typeof en> = {
  'brand.homeLabel': 'Postqron, inicio',
  'links.features': 'Funciones',
  'links.pricing': 'Precios',
  'links.faq': 'Preguntas frecuentes',
  'cta.start': 'Empieza ahora',
  'nav.primaryLabel': 'Navegación principal',
  'nav.mobileLabel': 'Navegación móvil',
  'menu.openLabel': 'Abrir el menú',
}

const fr: CatalogShape<typeof en> = {
  'brand.homeLabel': 'Postqron, accueil',
  'links.features': 'Fonctionnalités',
  'links.pricing': 'Tarifs',
  'links.faq': 'FAQ',
  'cta.start': 'Commencer',
  'nav.primaryLabel': 'Navigation principale',
  'nav.mobileLabel': 'Navigation mobile',
  'menu.openLabel': 'Ouvrir le menu',
}

const de: CatalogShape<typeof en> = {
  'brand.homeLabel': 'Postqron, Startseite',
  'links.features': 'Funktionen',
  'links.pricing': 'Preise',
  'links.faq': 'FAQ',
  'cta.start': 'Jetzt starten',
  'nav.primaryLabel': 'Hauptnavigation',
  'nav.mobileLabel': 'Mobile Navigation',
  'menu.openLabel': 'Menü öffnen',
}

export const MARKETING_NAV_CATALOGS = defineCatalogs({ en, it, es, fr, de })

export type MarketingNavMessageKey = keyof typeof en
