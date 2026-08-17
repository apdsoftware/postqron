export const PUBLIC_PAGE_IDS = ['features', 'faq', 'contact'] as const

export type PublicPageId = (typeof PUBLIC_PAGE_IDS)[number]

export function isPublicPageId(value: unknown): value is PublicPageId {
  return typeof value === 'string' && (PUBLIC_PAGE_IDS as readonly string[]).includes(value)
}
