import {
  DEFAULT_LEGAL_LOCALE,
  LEGAL_LOCALES,
  type DocumentType,
  type GateAudit,
  type LegalArtifact,
  type LegalLocale,
  type LegalRelease,
  type LegalReleaseInput,
  type PublishedLegalDocument,
} from './types.ts'
import {
  artifactKey,
  auditLegalRelease,
  compareVersions,
  normalizeLegalLocale,
} from './validation.ts'

function cloneArtifact(artifact: LegalArtifact): Readonly<LegalArtifact> {
  return Object.freeze({ ...artifact })
}

function permanentUrl(artifact: LegalArtifact): string {
  return `/legal/${artifact.document}/${artifact.version}?locale=${artifact.locale}`
}

export class LegalRepository {
  readonly audit: GateAudit
  readonly ready: boolean
  readonly #artifacts: ReadonlyMap<string, Readonly<LegalArtifact>>
  readonly #releases: readonly Readonly<LegalRelease>[]

  private constructor(input: LegalReleaseInput, audit: GateAudit) {
    this.audit = audit
    this.ready = audit.ready
    this.#artifacts = new Map(
      audit.ready
        ? input.artifacts.map(artifact => [artifactKey(artifact), cloneArtifact(artifact)])
        : [],
    )
    this.#releases = audit.ready
      ? Object.freeze(input.releases.map(release => Object.freeze({
          ...release,
          artifacts: Object.freeze(release.artifacts.map(item => Object.freeze({ ...item }))),
          evidenceIds: Object.freeze([...release.evidenceIds]),
        })))
      : Object.freeze([])
    Object.freeze(this)
  }

  static async create(input: LegalReleaseInput): Promise<LegalRepository> {
    return new LegalRepository(input, await auditLegalRelease(input))
  }

  current(
    document: DocumentType,
    requestedLocale: unknown,
    at = new Date().toISOString(),
  ): PublishedLegalDocument | undefined {
    const release = this.#effectiveReleases(at).at(-1)
    if (!release) {
      return undefined
    }
    return this.#fromRelease(release, document, requestedLocale)
  }

  version(
    document: DocumentType,
    version: string,
    requestedLocale: unknown,
    at = new Date().toISOString(),
  ): PublishedLegalDocument | undefined {
    const releases = this.#effectiveReleases(at)
    for (const release of [...releases].reverse()) {
      const selected = this.#fromRelease(release, document, requestedLocale, version)
      if (selected) {
        return selected
      }
    }
    return undefined
  }

  history(
    document: DocumentType,
    requestedLocale: unknown,
    at = new Date().toISOString(),
  ): readonly PublishedLegalDocument[] {
    const versions = new Map<string, PublishedLegalDocument>()
    for (const release of this.#effectiveReleases(at)) {
      const artifact = this.#fromRelease(release, document, requestedLocale)
      if (artifact) {
        versions.set(artifact.version, artifact)
      }
    }
    return Object.freeze(
      [...versions.values()].sort((left, right) =>
        compareVersions(left.version, right.version)),
    )
  }

  #effectiveReleases(at: string): readonly Readonly<LegalRelease>[] {
    if (!this.ready) {
      return []
    }
    const instant = new Date(at)
    if (Number.isNaN(instant.valueOf())) {
      return []
    }
    return this.#releases.filter(release => new Date(release.effectiveAt) <= instant)
  }

  #fromRelease(
    release: Readonly<LegalRelease>,
    document: DocumentType,
    requestedLocale: unknown,
    exactVersion?: string,
  ): PublishedLegalDocument | undefined {
    const locale = normalizeLegalLocale(requestedLocale)
    const requestedBase = typeof requestedLocale === 'string'
      ? requestedLocale.trim().toLowerCase().split(/[-_]/u, 1)[0] ?? ''
      : ''
    const requestedSupported =
      (LEGAL_LOCALES as readonly string[]).includes(requestedBase)
    const findReference = (candidate: LegalLocale) =>
      release.artifacts.find(reference =>
        reference.document === document
        && reference.locale === candidate
        && (!exactVersion || reference.version === exactVersion))
    const reference = findReference(locale) || findReference(DEFAULT_LEGAL_LOCALE)
    if (!reference) {
      return undefined
    }
    const artifact = this.#artifacts.get(artifactKey(reference))
    if (!artifact) {
      return undefined
    }
    return Object.freeze({
      ...artifact,
      requestedLocale: locale,
      fallbackUsed: !requestedSupported || artifact.locale !== locale,
      permanentUrl: permanentUrl(artifact),
    })
  }
}
