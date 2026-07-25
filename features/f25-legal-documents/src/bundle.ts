import { LegalRepository } from './repository.ts'
import type { LegalReleaseInput } from './types.ts'

// Deliberately empty. Counsel-approved artifacts and the release record must
// replace this input in a separately reviewed change before publication.
export const BUNDLED_LEGAL_RELEASE: LegalReleaseInput = Object.freeze({
  artifacts: Object.freeze([]),
  evidence: Object.freeze([]),
  releases: Object.freeze([]),
})

let repositoryPromise: Promise<LegalRepository> | undefined

export function loadBundledRepository(): Promise<LegalRepository> {
  repositoryPromise ??= LegalRepository.create(BUNDLED_LEGAL_RELEASE)
  return repositoryPromise
}
