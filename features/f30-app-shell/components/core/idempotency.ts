export interface MutationIntent<T> {
  readonly fingerprint: string
  readonly key: string
  readonly payload: T
}

function canonicalValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(canonicalValue)
  }
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .filter(([, item]) => item !== undefined)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, item]) => [key, canonicalValue(item)]),
    )
  }
  return value
}

export function mutationFingerprint(value: unknown): string {
  return JSON.stringify(canonicalValue(value))
}

export function createBrowserSafeIdempotencyKey(): string {
  const crypto = globalThis.crypto
  if (!crypto) {
    throw new Error('F30_SECURE_RANDOM_UNAVAILABLE')
  }
  if (typeof crypto.randomUUID === 'function') {
    return `f30_${crypto.randomUUID()}`
  }
  const bytes = crypto.getRandomValues(new Uint8Array(24))
  return `f30_${Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')}`
}

export function mutationIntent<T>(
  current: MutationIntent<T> | undefined,
  payload: T,
): MutationIntent<T> {
  const fingerprint = mutationFingerprint(payload)
  if (current?.fingerprint === fingerprint) {
    return current
  }
  return Object.freeze({
    fingerprint,
    key: createBrowserSafeIdempotencyKey(),
    payload,
  })
}
