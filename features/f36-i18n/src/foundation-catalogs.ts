import {
  defineCatalogs,
  type CatalogShape,
} from './catalog.ts'

const en = {
  'languageSwitcher.label': 'Language',
  'languageSwitcher.changed': 'Language changed to {language}',
  'language.en': 'English',
  'language.it': 'Italiano',
  'language.es': 'Español',
  'language.fr': 'Français',
  'language.de': 'Deutsch',
  'example.items': {
    one: '{count} item',
    other: '{count} items',
  },
} as const

const it: CatalogShape<typeof en> = {
  'languageSwitcher.label': 'Lingua',
  'languageSwitcher.changed': 'Lingua impostata su {language}',
  'language.en': 'English',
  'language.it': 'Italiano',
  'language.es': 'Español',
  'language.fr': 'Français',
  'language.de': 'Deutsch',
  'example.items': {
    one: '{count} elemento',
    other: '{count} elementi',
  },
}

const es: CatalogShape<typeof en> = {
  'languageSwitcher.label': 'Idioma',
  'languageSwitcher.changed': 'Idioma cambiado a {language}',
  'language.en': 'English',
  'language.it': 'Italiano',
  'language.es': 'Español',
  'language.fr': 'Français',
  'language.de': 'Deutsch',
  'example.items': {
    one: '{count} elemento',
    other: '{count} elementos',
  },
}

const fr: CatalogShape<typeof en> = {
  'languageSwitcher.label': 'Langue',
  'languageSwitcher.changed': 'Langue définie sur {language}',
  'language.en': 'English',
  'language.it': 'Italiano',
  'language.es': 'Español',
  'language.fr': 'Français',
  'language.de': 'Deutsch',
  'example.items': {
    one: '{count} élément',
    other: '{count} éléments',
  },
}

const de: CatalogShape<typeof en> = {
  'languageSwitcher.label': 'Sprache',
  'languageSwitcher.changed': 'Sprache geändert zu {language}',
  'language.en': 'English',
  'language.it': 'Italiano',
  'language.es': 'Español',
  'language.fr': 'Français',
  'language.de': 'Deutsch',
  'example.items': {
    one: '{count} Element',
    other: '{count} Elemente',
  },
}

export const FOUNDATION_CATALOGS = defineCatalogs({ en, it, es, fr, de })

export type FoundationCatalog = typeof en
export type FoundationMessageKey = keyof FoundationCatalog
