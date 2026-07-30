export type EmailVerificationResult = 'invalid' | 'no-token' | 'verified'
export type EmailVerificationResendResult = Readonly<{
  email: string
  status: 'error' | 'success'
}>

export function emailVerificationDataKey(componentId: string): string {
  return `postqron-email-verification:${componentId}`
}

export async function completeEmailVerification(
  token: string,
  verify: (token: string) => Promise<void>,
): Promise<EmailVerificationResult> {
  const normalized = token.trim()
  if (!normalized) {
    return 'no-token'
  }

  try {
    await verify(normalized)
    return 'verified'
  } catch {
    return 'invalid'
  }
}

export async function requestEmailVerification(
  email: string,
  resend: (email: string) => Promise<void>,
): Promise<EmailVerificationResendResult> {
  try {
    await resend(email)
    return { email: '', status: 'success' }
  } catch {
    return { email, status: 'error' }
  }
}

export function withoutEmailVerificationToken(location: string): string {
  const url = new URL(location)
  url.searchParams.delete('token')
  return `${url.pathname}${url.search}${url.hash}`
}

export function withoutEmailVerificationTokenInHistoryState(
  state: unknown,
  safeLocation: string,
): unknown {
  if (
    !state
    || typeof state !== 'object'
    || Array.isArray(state)
    || !('current' in state)
    || typeof state.current !== 'string'
  ) {
    return state
  }
  return {
    ...state,
    current: safeLocation,
  }
}
