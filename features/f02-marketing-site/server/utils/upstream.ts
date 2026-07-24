export function upstreamUrl(path: string): string {
  const config = useRuntimeConfig()
  return new URL(path, `${config.apiBase}/`).toString()
}

export function forwardedHeaders(event: Parameters<typeof getRequestHeader>[0]) {
  const headers: Record<string, string> = {}
  for (const name of ['authorization', 'cookie', 'idempotency-key']) {
    const value = getRequestHeader(event, name)
    if (value) {
      headers[name] = value
    }
  }
  return headers
}

export function normalizeUpstreamError(error: unknown): never {
  const candidate = error as {
    status?: number
    statusCode?: number
    data?: unknown
    message?: string
  }
  throw createError({
    statusCode: candidate.statusCode || candidate.status || 502,
    statusMessage: candidate.message || 'Servizio temporaneamente non disponibile',
    data: candidate.data,
  })
}
