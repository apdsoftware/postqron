import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  DOCUMENT_TYPES,
  LEGAL_LOCALES,
  type ArtifactStatus,
  type ChangeType,
  type LegalArtifact,
} from './types.ts'
import { sha256 } from './validation.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')

export const DRAFTS_ROOT = resolve(featureRoot, 'content/drafts')

const FRONTMATTER_PATTERN = /^---\r?\n([\s\S]*?)\r?\n---\r?\n([\s\S]*)$/u

const REQUIRED_FRONTMATTER_FIELDS = [
  'title',
  'controllerName',
  'contactEmail',
  'status',
  'changeType',
  'revisionSummary',
  'version',
] as const

interface DraftFrontmatter {
  title: string
  controllerName: string
  contactEmail: string
  status: string
  changeType: string
  revisionSummary: string
  version: string
}

export function draftPath(document: string, locale: string): string {
  return resolve(DRAFTS_ROOT, document, `${locale}.md`)
}

function unquote(value: string): string {
  const trimmed = value.trim()
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    return trimmed.slice(1, -1)
  }
  return trimmed
}

export function parseDraftFile(
  raw: string,
  sourcePath: string,
): { frontmatter: DraftFrontmatter, body: string } {
  const match = FRONTMATTER_PATTERN.exec(raw)
  if (!match) {
    throw new Error(`missing frontmatter block in ${sourcePath}`)
  }
  const [, block, rest] = match
  const fields: Record<string, string> = {}
  for (const line of (block ?? '').split(/\r?\n/u)) {
    if (!line.trim()) {
      continue
    }
    const separator = line.indexOf(':')
    if (separator === -1) {
      throw new Error(`invalid frontmatter line "${line}" in ${sourcePath}`)
    }
    fields[line.slice(0, separator).trim()] = unquote(line.slice(separator + 1))
  }
  for (const required of REQUIRED_FRONTMATTER_FIELDS) {
    if (!fields[required]) {
      throw new Error(`missing required frontmatter field "${required}" in ${sourcePath}`)
    }
  }
  return {
    frontmatter: fields as unknown as DraftFrontmatter,
    body: (rest ?? '').trim(),
  }
}

/**
 * Loads the committed draft corpus for tests and previews only. Never wired
 * into `src/api.ts` or the Vue page — those keep reading `BUNDLED_LEGAL_RELEASE`,
 * which stays empty until a legally approved release replaces it.
 */
export async function loadDraftArtifacts(): Promise<LegalArtifact[]> {
  const artifacts: LegalArtifact[] = []
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const path = draftPath(document, locale)
      const raw = await readFile(path, 'utf8')
      const { frontmatter, body } = parseDraftFile(raw, path)
      artifacts.push({
        document,
        locale,
        jurisdiction: 'IT',
        version: frontmatter.version,
        status: frontmatter.status as ArtifactStatus,
        title: frontmatter.title,
        controllerName: frontmatter.controllerName,
        contactEmail: frontmatter.contactEmail,
        content: body,
        digestSha256: await sha256(body),
        approvalReference: '',
        approvedAt: '',
        publishedAt: '',
        effectiveAt: '',
        changeType: frontmatter.changeType as ChangeType,
        revisionSummary: frontmatter.revisionSummary,
      })
    }
  }
  return artifacts
}
