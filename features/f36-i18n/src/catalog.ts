import { I18nError } from './errors.ts'
import {
  FALLBACK_LOCALE,
  SUPPORTED_LOCALES,
  type Locale,
} from './locales.ts'

export type MessageParameters = Readonly<Record<string, string | number>>

export type PluralCategory =
  | 'zero'
  | 'one'
  | 'two'
  | 'few'
  | 'many'
  | 'other'

export type PluralMessage = Readonly<
  Partial<Record<PluralCategory | `=${number}`, string>>
  & { other: string }
>

export type CatalogMessage = string | PluralMessage
export type Catalog = Readonly<Record<string, CatalogMessage>>

export type CatalogShape<Reference extends Catalog> = {
  readonly [Key in keyof Reference]:
    Reference[Key] extends string ? string : PluralMessage
}

export type CatalogSet<Reference extends Catalog = Catalog> = Readonly<
  { en: Reference }
  & { [CurrentLocale in Exclude<Locale, 'en'>]: CatalogShape<Reference> }
>

export function defineCatalogs<const Reference extends Catalog>(
  catalogs: CatalogSet<Reference>,
): CatalogSet<Reference> {
  validateCatalogs(catalogs)
  return catalogs
}

const placeholderPattern = /\{([A-Za-z][A-Za-z0-9_]*)\}/gu

function variants(message: CatalogMessage): readonly string[] {
  return typeof message === 'string'
    ? [message]
    : Object.values(message).filter((value): value is string =>
        typeof value === 'string')
}

function placeholders(message: CatalogMessage): Set<string> {
  const result = new Set<string>()
  for (const variant of variants(message)) {
    for (const match of variant.matchAll(placeholderPattern)) {
      result.add(match[1]!)
    }
  }
  return result
}

function sameSet(left: Set<string>, right: Set<string>): boolean {
  return left.size === right.size && [...left].every(value => right.has(value))
}

function assertSafeMessage(
  locale: Locale,
  key: string,
  message: CatalogMessage,
): void {
  for (const value of variants(message)) {
    if (value.includes('<') || value.includes('>')) {
      throw new I18nError(
        'I18N_CATALOG_UNSAFE_HTML',
        `Catalog ${locale} key ${key} contains HTML-like markup`,
      )
    }
  }
  if (typeof message !== 'string' && typeof message.other !== 'string') {
    throw new I18nError(
      'I18N_CATALOG_MISSING_KEY',
      `Catalog ${locale} plural key ${key} requires an other variant`,
    )
  }
}

export function validateCatalogs(catalogs: Readonly<Record<Locale, Catalog>>): void {
  const reference = catalogs[FALLBACK_LOCALE]
  const referenceKeys = Object.keys(reference).sort()
  const referenceSet = new Set(referenceKeys)

  for (const locale of SUPPORTED_LOCALES) {
    const catalog = catalogs[locale]
    const keys = Object.keys(catalog)
    const keySet = new Set(keys)

    for (const key of referenceKeys) {
      if (!keySet.has(key)) {
        throw new I18nError(
          'I18N_CATALOG_MISSING_KEY',
          `Catalog ${locale} is missing key ${key}`,
        )
      }
    }
    for (const key of keys) {
      if (!referenceSet.has(key)) {
        throw new I18nError(
          'I18N_CATALOG_ORPHAN_KEY',
          `Catalog ${locale} has orphan key ${key}`,
        )
      }
    }
    for (const key of referenceKeys) {
      const referenceMessage = reference[key]!
      const message = catalog[key]!
      assertSafeMessage(locale, key, message)
      if (
        typeof referenceMessage === 'string'
        !== (typeof message === 'string')
      ) {
        throw new I18nError(
          'I18N_CATALOG_PLACEHOLDER_MISMATCH',
          `Catalog ${locale} key ${key} changes its message kind`,
        )
      }
      if (!sameSet(placeholders(referenceMessage), placeholders(message))) {
        throw new I18nError(
          'I18N_CATALOG_PLACEHOLDER_MISMATCH',
          `Catalog ${locale} key ${key} has different placeholders`,
        )
      }
    }
  }
}

function selectPlural(
  locale: Locale,
  message: PluralMessage,
  count: number,
): string {
  const exact = message[`=${count}`]
  if (exact !== undefined) {
    return exact
  }
  const category = new Intl.PluralRules(locale).select(count)
  return message[category] ?? message.other
}

function interpolate(
  message: string,
  parameters: MessageParameters,
): string {
  return message.replace(placeholderPattern, (_placeholder, name: string) => {
    const value = parameters[name]
    if (value === undefined) {
      throw new I18nError(
        'I18N_MESSAGE_MISSING_PARAMETER',
        `Missing interpolation parameter: ${name}`,
      )
    }
    return String(value)
  })
}

export function translateCatalog(
  catalog: Catalog,
  locale: Locale,
  key: string,
  parameters: MessageParameters = {},
): string {
  const message = catalog[key]
  if (message === undefined) {
    throw new I18nError(
      'I18N_MESSAGE_UNKNOWN',
      `Unknown translation key: ${key}`,
    )
  }
  const selected = typeof message === 'string'
    ? message
    : selectPlural(locale, message, Number(parameters.count ?? 0))
  return interpolate(selected, parameters)
}

export class CatalogRegistry {
  readonly #catalogs = new Map<string, CatalogSet>()

  register<const Reference extends Catalog>(
    namespace: string,
    catalogs: CatalogSet<Reference>,
  ): void {
    if (!namespace || this.#catalogs.has(namespace)) {
      throw new I18nError(
        'I18N_CATALOG_ORPHAN_KEY',
        `Catalog namespace is empty or already registered: ${namespace}`,
      )
    }
    validateCatalogs(catalogs)
    this.#catalogs.set(namespace, catalogs)
  }

  translate(
    locale: Locale,
    namespacedKey: string,
    parameters: MessageParameters = {},
  ): string {
    const separator = namespacedKey.indexOf('.')
    if (separator <= 0) {
      throw new I18nError(
        'I18N_MESSAGE_UNKNOWN',
        `Translation keys must include a namespace: ${namespacedKey}`,
      )
    }
    const namespace = namespacedKey.slice(0, separator)
    const key = namespacedKey.slice(separator + 1)
    const catalogs = this.#catalogs.get(namespace)
    if (!catalogs) {
      throw new I18nError(
        'I18N_MESSAGE_UNKNOWN',
        `Unknown translation namespace: ${namespace}`,
      )
    }
    return translateCatalog(catalogs[locale], locale, key, parameters)
  }
}
