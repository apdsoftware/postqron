import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  CatalogRegistry,
  FOUNDATION_CATALOGS,
  createFormatters,
  createLanguageSwitcherModel,
  translateCatalog,
  validateCatalogs,
  type Catalog,
  type CatalogMessage,
  type Locale,
} from '../src/index.ts'

test('foundation catalogs are complete and translate interpolation and plurals', () => {
  validateCatalogs(FOUNDATION_CATALOGS)
  assert.equal(
    translateCatalog(
      FOUNDATION_CATALOGS.fr,
      'fr',
      'languageSwitcher.changed',
      { language: 'Français' },
    ),
    'Langue définie sur Français',
  )
  assert.equal(
    translateCatalog(FOUNDATION_CATALOGS.en, 'en', 'example.items', { count: 1 }),
    '1 item',
  )
  assert.equal(
    translateCatalog(FOUNDATION_CATALOGS.de, 'de', 'example.items', { count: 3 }),
    '3 Elemente',
  )
})

function invalidCatalogs(
  locale: Locale,
  catalog: Catalog,
): Record<Locale, Catalog> {
  return {
    ...FOUNDATION_CATALOGS,
    [locale]: catalog,
  }
}

test('catalog validation rejects missing, orphan, unsafe, and mismatched messages', () => {
  const missing: Record<string, CatalogMessage> = { ...FOUNDATION_CATALOGS.it }
  delete missing['language.en']
  assert.throws(
    () => validateCatalogs(invalidCatalogs('it', missing)),
    (error: unknown) => errorCode(error) === 'I18N_CATALOG_MISSING_KEY',
  )
  assert.throws(
    () => validateCatalogs(invalidCatalogs('es', {
      ...FOUNDATION_CATALOGS.es,
      orphan: 'huérfano',
    })),
    (error: unknown) => errorCode(error) === 'I18N_CATALOG_ORPHAN_KEY',
  )
  assert.throws(
    () => validateCatalogs(invalidCatalogs('fr', {
      ...FOUNDATION_CATALOGS.fr,
      'languageSwitcher.label': '<strong>Langue</strong>',
    })),
    (error: unknown) => errorCode(error) === 'I18N_CATALOG_UNSAFE_HTML',
  )
  assert.throws(
    () => validateCatalogs(invalidCatalogs('de', {
      ...FOUNDATION_CATALOGS.de,
      'languageSwitcher.changed': 'Sprache geändert',
    })),
    (error: unknown) => errorCode(error) === 'I18N_CATALOG_PLACEHOLDER_MISMATCH',
  )
})

function errorCode(error: unknown): unknown {
  return error instanceof Error && 'code' in error ? error.code : undefined
}

test('feature catalogs register by namespace without a central key registry', () => {
  const registry = new CatalogRegistry()
  registry.register('feature-example', FOUNDATION_CATALOGS)
  assert.equal(
    registry.translate('it', 'feature-example.languageSwitcher.label'),
    'Lingua',
  )
  assert.throws(
    () => registry.translate('it', 'unknown.title'),
    (error: unknown) => errorCode(error) === 'I18N_MESSAGE_UNKNOWN',
  )
})

test('Intl formatters use active locale while inputs remain language-independent', () => {
  const instant = '2026-07-24T18:30:00.000Z'
  const english = createFormatters('en')
  const german = createFormatters('de')

  assert.notEqual(
    english.number(1234.5, { minimumFractionDigits: 2 }),
    german.number(1234.5, { minimumFractionDigits: 2 }),
  )
  assert.match(english.currency(42, 'USD'), /\$/u)
  assert.match(german.currency(42, 'EUR'), /€/u)
  assert.equal(
    english.timeZone(instant, 'UTC', {
      hour: '2-digit',
      hourCycle: 'h23',
      minute: '2-digit',
    }),
    '18:30',
  )
  assert.equal(
    english.timeZone(instant, 'America/Santo_Domingo', {
      hour: '2-digit',
      hourCycle: 'h23',
      minute: '2-digit',
    }),
    '14:30',
  )
})

test('language switcher preserves query and exposes a native select', async () => {
  const model = createLanguageSwitcherModel(
    'it',
    '/it/calendar?view=month&channel=2',
  )
  assert.equal(model.items.length, 5)
  assert.equal(model.items.find(item => item.locale === 'en')?.href, '/en/calendar?view=month&channel=2')
  assert.equal(model.items.find(item => item.locale === 'fr')?.href, '/fr/calendar?view=month&channel=2')
  assert.equal(model.items.find(item => item.locale === 'it')?.active, true)
  assert.deepEqual(model.accessibility, {
    activation: 'native-select',
    currentAttribute: 'value',
    statusLive: 'polite',
  })

  const component = await readFile(
    new URL('../components/LanguageSwitcher.vue', import.meta.url),
    'utf8',
  )
  assert.match(component, /<select[\s\S]*:aria-label=/u)
  assert.match(component, /<option/u)
  assert.doesNotMatch(component, /<a/u)
  assert.match(component, /aria-live="polite"/u)
  assert.match(component, /:focus-within/u)
})
