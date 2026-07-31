import {
  normalizeSocialApiError,
  SocialApiError,
} from './social-api.ts'
import {
  parseSocialSelection,
  type SocialSelection,
} from './social-connections.ts'

export const SOCIAL_OAUTH_CALLBACK_PARAMETERS = [
  'state',
  'code',
  'iss',
  'error',
] as const

type QueryValue = string | readonly (string | null)[] | null | undefined

export interface SocialOAuthCallbackInput {
  code: string
  error: string
  iss?: string
  state: string
}

function singleQueryValue(value: QueryValue, name: string): string {
  if (Array.isArray(value)) {
    throw new SocialApiError({
      code: 'social_invalid_callback_parameters',
      kind: 'invalid',
      message: `OAuth callback parameter ${name} must occur once`,
      retryable: false,
    })
  }
  return typeof value === 'string' ? value : ''
}

export function socialOAuthCallbackInput(
  query: Readonly<Record<string, QueryValue>>,
): SocialOAuthCallbackInput {
  const state = singleQueryValue(query.state, 'state')
  const code = singleQueryValue(query.code, 'code')
  const error = singleQueryValue(query.error, 'error')
  const iss = singleQueryValue(query.iss, 'iss')
  if (!state || (!code && !error) || (code && error)) {
    throw new SocialApiError({
      code: 'social_invalid_callback_parameters',
      kind: 'invalid',
      message: 'OAuth callback requires state and exactly one result',
      retryable: false,
    })
  }
  return { state, code, error, ...(iss ? { iss } : {}) }
}

export function socialCallbackHandoffDocument(value: unknown): string {
  if (value instanceof SocialApiError) {
    return JSON.stringify({
      code: value.code,
      message: value.message,
      retryable: value.retryable,
    })
  }
  return JSON.stringify(value)
}

export function withoutSocialOAuthCallbackParameters(
  query: Readonly<Record<string, QueryValue>>,
): Record<string, QueryValue> {
  const cleanQuery = { ...query }
  for (const parameter of SOCIAL_OAUTH_CALLBACK_PARAMETERS) {
    delete cleanQuery[parameter]
  }
  return cleanQuery
}

export function parseSocialCallbackDocument(
  value: string,
): SocialSelection {
  const trimmed = value.trim()
  if (trimmed === '') {
    throw new SocialApiError({
      code: 'social_invalid_callback_document',
      kind: 'unavailable',
      message: 'The callback handoff document is empty',
      retryable: true,
    })
  }

  let payload: unknown
  try {
    payload = JSON.parse(trimmed)
  } catch (error) {
    throw new SocialApiError({
      cause: error,
      code: 'social_invalid_callback_document',
      kind: 'unavailable',
      message: 'The callback handoff document is not valid JSON',
      retryable: true,
    })
  }

  try {
    return parseSocialSelection(payload)
  } catch {
    throw normalizeSocialApiError({ data: payload })
  }
}
