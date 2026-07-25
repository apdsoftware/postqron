import { createHash } from 'node:crypto'

const CONTENT_SECURITY_POLICY_DIRECTIVES = [
  "default-src 'self'",
  "img-src 'self' data:",
  "style-src 'self' 'unsafe-inline'",
  "script-src 'self'",
  "connect-src 'self'",
] as const

export const STATIC_CONTENT_SECURITY_POLICY
  = CONTENT_SECURITY_POLICY_DIRECTIVES.join('; ')

export function inlineScriptHashes(chunks: readonly string[]): string[] {
  const hashes = new Set<string>()
  const scriptPattern = /<script\b([^>]*)>([\s\S]*?)<\/script\s*>/giu

  for (const chunk of chunks) {
    for (const match of chunk.matchAll(scriptPattern)) {
      const attributes = match[1]!
      if (/\bsrc(?:\s*=|\s|$)/iu.test(attributes)) {
        continue
      }
      const digest = createHash('sha256')
        .update(match[2]!, 'utf8')
        .digest('base64')
      hashes.add(`'sha256-${digest}'`)
    }
  }

  return [...hashes]
}

export function contentSecurityPolicyForHtml(
  chunks: readonly string[],
): string {
  const hashes = inlineScriptHashes(chunks)

  return CONTENT_SECURITY_POLICY_DIRECTIVES
    .map(directive => directive === "script-src 'self'"
      ? [directive, ...hashes].join(' ')
      : directive)
    .join('; ')
}
