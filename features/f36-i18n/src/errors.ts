export const I18N_ERROR_CODES = [
  'I18N_UNSUPPORTED_LOCALE',
  'I18N_INVALID_LOCAL_URL',
  'I18N_CATALOG_MISSING_KEY',
  'I18N_CATALOG_ORPHAN_KEY',
  'I18N_CATALOG_UNSAFE_HTML',
  'I18N_CATALOG_PLACEHOLDER_MISMATCH',
  'I18N_MESSAGE_MISSING_PARAMETER',
  'I18N_MESSAGE_UNKNOWN',
  'I18N_PROFILE_PERSIST_FAILED',
  'I18N_INVALID_SSR_STATE',
  'I18N_RUNTIME_UNAVAILABLE',
] as const

export type I18nErrorCode = typeof I18N_ERROR_CODES[number]

export class I18nError extends Error {
  readonly code: I18nErrorCode

  constructor(code: I18nErrorCode, message: string, cause?: unknown) {
    super(message, cause === undefined ? undefined : { cause })
    this.name = 'I18nError'
    this.code = code
  }
}

export function isI18nError(error: unknown): error is I18nError {
  return error instanceof I18nError
}
