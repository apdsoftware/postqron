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
