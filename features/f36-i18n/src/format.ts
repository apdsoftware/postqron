import type { Locale } from './locales.ts'

export interface I18nFormatters {
  date(
    value: Date | number | string,
    options?: Intl.DateTimeFormatOptions,
  ): string
  number(value: number, options?: Intl.NumberFormatOptions): string
  currency(
    value: number,
    currency: string,
    options?: Omit<Intl.NumberFormatOptions, 'currency' | 'style'>,
  ): string
  timeZone(
    value: Date | number | string,
    timeZone: string,
    options?: Omit<Intl.DateTimeFormatOptions, 'timeZone'>,
  ): string
}

function instant(value: Date | number | string): Date {
  return value instanceof Date ? value : new Date(value)
}

export function createFormatters(locale: Locale): I18nFormatters {
  return {
    date(value, options = {}) {
      return new Intl.DateTimeFormat(locale, options).format(instant(value))
    },
    number(value, options = {}) {
      return new Intl.NumberFormat(locale, options).format(value)
    },
    currency(value, currency, options = {}) {
      return new Intl.NumberFormat(locale, {
        ...options,
        style: 'currency',
        currency,
      }).format(value)
    },
    timeZone(value, timeZone, options = {}) {
      return new Intl.DateTimeFormat(locale, {
        ...options,
        timeZone,
      }).format(instant(value))
    },
  }
}
