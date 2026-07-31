import {
  normalizeSocialApiError,
  SocialApiError,
} from './social-api.ts'
import {
  parseSocialSelection,
  type SocialSelection,
} from './social-connections.ts'

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
