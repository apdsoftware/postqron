// Regenerates `src/bundle.ts` from the approved corpus in `content/drafts/**`,
// the immutable historical corpus in `content/history/**`, and the verified
// provider evidence in `content/subprocessors.json`.
//
// Two releases are embedded:
// - release 0.1 (market IT), approved under reference
//   LEGAL-APPROVAL-2026-07-25-F25
//   (https://github.com/apdsoftware/postqron/issues/134#issuecomment-5080412206),
//   whose `terms` artifacts are read from the immutable, never-edited copy in
//   `content/history/terms/**` so their content and digest never change;
// - release 0.2 (market IT), which supersedes release 0.1 only for `terms`
//   (F25 alignment with decision D09 and the F10 four-plan catalog), approved
//   under the Product Owner's attestation of external legal review recorded at
//   https://github.com/apdsoftware/postqron/issues/179#issuecomment-5090088911.
//   `privacy`, `cookies`, `dpa`, and `subprocessors` are unchanged since 0.1
//   and are referenced again with their original approval metadata, not
//   re-approved.
//
// Run with: node --experimental-strip-types scripts/build-bundle.ts
//
// The generated `src/bundle.ts` embeds the releases as a plain JSON literal
// so it stays free of Node's `fs`/`path`/`url` built-ins, which must never
// reach the browser bundle (see `src/content.ts`). Do not hand-edit the
// generated file; change this script or the source corpus and regenerate
// instead.

import { readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { loadDraftArtifacts, parseDraftFile } from '../src/content.ts'
import { sha256 } from '../src/validation.ts'
import { LEGAL_LOCALES } from '../src/types.ts'
import type {
  LegalArtifact,
  LegalEvidence,
  LegalRelease,
  LegalReleaseInput,
} from '../src/types.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')

const RELEASE_0_1_APPROVAL_REFERENCE = 'LEGAL-APPROVAL-2026-07-25-F25'
const RELEASE_0_1_APPROVED_AT = '2026-07-25T20:16:56.000Z'
const RELEASE_0_1_PUBLISHED_AT = '2026-07-25T20:16:56.000Z'
const RELEASE_0_1_EFFECTIVE_AT = '2026-07-25T20:16:56.000Z'

// Product Owner's attestation, on the F25 issue itself, that the Terms
// revision required by issue #179 (aligning Plans and pricing with D09 and
// the F10 four-plan catalog) was reviewed and approved by external legal
// counsel. Counsel's identity and supporting documentation are kept off
// repository for confidentiality; this GitHub comment, with its
// platform-recorded timestamp, is the verifiable approval reference.
const RELEASE_0_2_APPROVAL_REFERENCE =
  'https://github.com/apdsoftware/postqron/issues/179#issuecomment-5090088911'
const RELEASE_0_2_APPROVED_AT = '2026-07-27T10:15:46.000Z'
const RELEASE_0_2_PUBLISHED_AT = '2026-07-27T10:15:46.000Z'
const RELEASE_0_2_EFFECTIVE_AT = '2026-07-27T10:15:46.000Z'

const EVIDENCE_VERIFIED_AT = '2026-07-25T00:00:00.000Z'

const EVIDENCE: readonly LegalEvidence[] = Object.freeze([
  {
    id: 'controller-identity:apdsoftware:2026-07-25',
    kind: 'controller_identity',
    reference: 'Apdsoftware di Carlo Zuffetti — Via C. Colombo 15, 24047 Treviglio (BG), Italia, P.IVA 03835250162, REA BG 431224',
    sourceUrl: 'https://mailronix.com/terms',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
  {
    id: 'cookie-inventory:postqron:2026-07-25',
    kind: 'cookie_inventory',
    reference: 'Cookie/local-storage inventory derived from features/f03-auth/http.go, features/f26-cookie-consent-api/http.go, features/f36-i18n/src/cookie.ts and features/f02-marketing-site/components/CookiePreferences.vue',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
  {
    id: 'retention-schedule:d05:2026-07-25',
    kind: 'retention_schedule',
    reference: 'docs/decisions/d05-data-operations.md — Policy di retention',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
  {
    id: 'postqron-dpa:v0.1:2026-07-25',
    kind: 'postqron_dpa',
    reference: 'Postqron Data Processing Agreement v0.1, released under LEGAL-APPROVAL-2026-07-25-F25 (this release)',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
  {
    id: 'mailronix-dpa:2026-07-25',
    kind: 'mailronix_dpa',
    reference: 'Mailronix (operated by Apdsoftware di Carlo Zuffetti) Terms, which state the DPA is an integral part of those Terms',
    sourceUrl: 'https://mailronix.com/terms',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
  {
    id: 'mailronix-subprocessors:2026-07-25',
    kind: 'mailronix_subprocessors',
    reference: 'Postqron subprocessor registry — features/f25-legal-documents/content/subprocessors.json',
    sourceUrl: 'https://mailronix.com/terms',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
  {
    id: 'mailronix-transfers:2026-07-25',
    kind: 'mailronix_transfers',
    reference: 'Mailronix processing location: Germany (Hetzner infrastructure; email delivery via AWS SES, Frankfurt) — EEA transfer mechanism, no third-country transfer',
    sourceUrl: 'https://mailronix.com/terms',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
  {
    id: 'paddle-terms:2026-07-25',
    kind: 'paddle_terms',
    reference: 'Paddle Data Processing Addendum',
    sourceUrl: 'https://www.paddle.com/legal/data-processing-addendum',
    verifiedAt: EVIDENCE_VERIFIED_AT,
  },
])

const HISTORY_ROOT = resolve(featureRoot, 'content/history')

function historyPath(document: string, locale: string): string {
  return resolve(HISTORY_ROOT, document, `${locale}.md`)
}

// Loads the immutable, never-edited terms/0.1 corpus preserved under
// `content/history/terms/**` before it was superseded by 0.2, and stamps it
// with the original release 0.1 approval metadata. Kept separate from
// `loadDraftArtifacts` (which always reads the current draft, now at 0.2) so
// the historical artifact's content and digest can never drift.
async function loadHistoricalTermsArtifacts(): Promise<LegalArtifact[]> {
  const artifacts: LegalArtifact[] = []
  for (const locale of LEGAL_LOCALES) {
    const path = historyPath('terms', locale)
    const raw = await readFile(path, 'utf8')
    const { frontmatter, body } = parseDraftFile(raw, path)
    artifacts.push({
      document: 'terms',
      locale,
      jurisdiction: frontmatter.jurisdiction as LegalArtifact['jurisdiction'],
      version: frontmatter.version,
      status: frontmatter.status as LegalArtifact['status'],
      title: frontmatter.title,
      controllerName: frontmatter.controllerName,
      contactEmail: frontmatter.contactEmail,
      content: body,
      digestSha256: await sha256(body),
      approvalReference: RELEASE_0_1_APPROVAL_REFERENCE,
      approvedAt: RELEASE_0_1_APPROVED_AT,
      publishedAt: RELEASE_0_1_PUBLISHED_AT,
      proposedEffectiveDate: frontmatter.proposedEffectiveDate,
      effectiveAt: RELEASE_0_1_EFFECTIVE_AT,
      changeType: frontmatter.changeType as LegalArtifact['changeType'],
      revisionSummary: frontmatter.revisionSummary,
    })
  }
  return artifacts
}

function toReferences(artifacts: readonly LegalArtifact[]) {
  return artifacts.map(item => ({
    document: item.document,
    locale: item.locale,
    version: item.version,
    digestSha256: item.digestSha256,
  }))
}

async function buildReleaseInput(): Promise<LegalReleaseInput> {
  // Current draft corpus, i.e. what is approved and effective today: terms is
  // now 0.2, while privacy/cookies/dpa/subprocessors remain the unchanged 0.1
  // artifacts approved under release 0.1.
  const currentDrafts = await loadDraftArtifacts({
    approvalReference: RELEASE_0_2_APPROVAL_REFERENCE,
    approvedAt: RELEASE_0_2_APPROVED_AT,
    publishedAt: RELEASE_0_2_PUBLISHED_AT,
    effectiveAt: RELEASE_0_2_EFFECTIVE_AT,
  })
  const newTerms = currentDrafts.filter(item => item.document === 'terms')

  const unchangedSince0_1 = await loadDraftArtifacts({
    approvalReference: RELEASE_0_1_APPROVAL_REFERENCE,
    approvedAt: RELEASE_0_1_APPROVED_AT,
    publishedAt: RELEASE_0_1_PUBLISHED_AT,
    effectiveAt: RELEASE_0_1_EFFECTIVE_AT,
  })
  const unchangedDocuments = unchangedSince0_1.filter(item => item.document !== 'terms')

  const historicalTerms = await loadHistoricalTermsArtifacts()

  const artifacts = [...historicalTerms, ...unchangedDocuments, ...newTerms]

  const release0_1: LegalRelease = {
    id: 'release-2026-07-25-F25-0.1',
    market: 'IT',
    version: '0.1',
    fallbackLocale: 'en',
    approvalReference: RELEASE_0_1_APPROVAL_REFERENCE,
    approvedAt: RELEASE_0_1_APPROVED_AT,
    effectiveAt: RELEASE_0_1_EFFECTIVE_AT,
    artifacts: toReferences([...historicalTerms, ...unchangedDocuments]),
    evidenceIds: EVIDENCE.map(item => item.id),
  }

  const release0_2: LegalRelease = {
    id: 'release-2026-07-27-F25-0.2',
    market: 'IT',
    version: '0.2',
    fallbackLocale: 'en',
    approvalReference: RELEASE_0_2_APPROVAL_REFERENCE,
    approvedAt: RELEASE_0_2_APPROVED_AT,
    effectiveAt: RELEASE_0_2_EFFECTIVE_AT,
    artifacts: toReferences([...newTerms, ...unchangedDocuments]),
    evidenceIds: EVIDENCE.map(item => item.id),
  }

  return {
    artifacts,
    evidence: EVIDENCE,
    releases: [release0_1, release0_2],
    marketAllowlist: [
      { market: 'IT', status: 'active', approvalReference: RELEASE_0_1_APPROVAL_REFERENCE },
    ],
  }
}

function renderBundle(input: LegalReleaseInput): string {
  const payload = JSON.stringify(input, null, 2)
  return `import { LegalRepository } from './repository.ts'
import type { LegalReleaseInput } from './types.ts'

// Generated by \`node --experimental-strip-types scripts/build-bundle.ts\`
// from the approved corpus in \`content/drafts/**\`, the immutable historical
// corpus in \`content/history/**\`, and the verified provider evidence in
// \`content/subprocessors.json\`. Release 0.1 was approved under reference
// LEGAL-APPROVAL-2026-07-25-F25
// (https://github.com/apdsoftware/postqron/issues/134#issuecomment-5080412206).
// Release 0.2 supersedes 0.1 for \`terms\` only (F25 alignment with D09 and the
// F10 four-plan catalog), approved under the Product Owner's attestation at
// https://github.com/apdsoftware/postqron/issues/179#issuecomment-5090088911.
// Digests are computed mechanically from the corpus; do not hand-edit this
// file — change the source corpus or the generation script and regenerate.
export const BUNDLED_LEGAL_RELEASE: LegalReleaseInput = Object.freeze(
${payload} as LegalReleaseInput,
)

let repositoryPromise: Promise<LegalRepository> | undefined

export function loadBundledRepository(): Promise<LegalRepository> {
  repositoryPromise ??= LegalRepository.create(BUNDLED_LEGAL_RELEASE)
  return repositoryPromise
}
`
}

const input = await buildReleaseInput()
const output = renderBundle(input)
await writeFile(resolve(featureRoot, 'src/bundle.ts'), output, 'utf8')
console.warn(`wrote ${input.artifacts.length} artifacts, ${input.evidence.length} evidence records, ${input.releases.length} release(s) to src/bundle.ts`)
