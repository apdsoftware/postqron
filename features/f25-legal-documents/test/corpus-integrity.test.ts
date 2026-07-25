import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { hasDraftingMarker } from '../src/index.ts'
import { loadDraftArtifacts } from '../src/content.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')

// The real, code-verified cookie/local-storage names Postqron sets. This list
// must be kept in sync with the source files cited below; it is the
// authoritative cross-check against the Cookie Policy's inventory.
const KNOWN_COOKIE_NAMES = [
  '__Host-postqron_session', // features/f03-auth/http.go, features/f26-cookie-consent-api/http.go
  '__Host-postqron_cookie_subject', // features/f26-cookie-consent-api/http.go
  'postqron_locale', // features/f36-i18n/src/cookie.ts
  'postqron.cookie-choice', // features/f02-marketing-site/components/CookiePreferences.vue (local storage)
]

// Third-party tracker names that must never appear in the Cookie Policy,
// since no such tracker is registered anywhere in the codebase.
const UNKNOWN_TRACKER_DENYLIST = [
  '_ga', '_gid', '_gat', '_fbp', '_fbc', 'fr=', 'hjid', 'hjSession',
  '__utma', '__utmb', 'mp_', 'ajs_',
]

test('no draft file contains a drafting marker or the deleted F13 placeholder text', async () => {
  const artifacts = await loadDraftArtifacts()
  for (const artifact of artifacts) {
    for (const [field, value] of [
      ['title', artifact.title],
      ['controllerName', artifact.controllerName],
      ['revisionSummary', artifact.revisionSummary],
      ['content', artifact.content],
    ] as const) {
      assert.equal(
        hasDraftingMarker(value),
        false,
        `${artifact.document}:${artifact.locale} ${field} contains a drafting marker`,
      )
    }
    assert.doesNotMatch(artifact.content, /BOZZA NON PUBBLICABILE/iu)
    assert.ok(artifact.content.trim().length >= 500)
  }
})

test('the Cookie Policy mentions every real cookie and no invented tracker', async () => {
  const artifacts = await loadDraftArtifacts()
  const cookiePolicies = artifacts.filter(item => item.document === 'cookies')
  assert.equal(cookiePolicies.length, 5)
  for (const artifact of cookiePolicies) {
    for (const name of KNOWN_COOKIE_NAMES) {
      assert.ok(
        artifact.content.includes(name),
        `${artifact.locale} cookie policy is missing real cookie ${name}`,
      )
    }
    for (const denied of UNKNOWN_TRACKER_DENYLIST) {
      assert.equal(
        artifact.content.toLowerCase().includes(denied.toLowerCase()),
        false,
        `${artifact.locale} cookie policy mentions an unrecognized tracker token ${denied}`,
      )
    }
  }
})

test('the subprocessor registry has verifiable, sourced entries with a consistent role model', async () => {
  const raw = await readFile(resolve(featureRoot, 'content/subprocessors.json'), 'utf8')
  const registry = JSON.parse(raw) as {
    entries: Array<{
      role: 'subprocessor' | 'independent_third_party'
      dpaReference: string | null
      sourceUrl: string
      sourceConsultedAt: string
      sourceVerified: boolean
    }>
  }
  assert.ok(registry.entries.length > 0)
  for (const entry of registry.entries) {
    assert.ok(entry.sourceUrl && entry.sourceUrl.trim().length > 0, 'sourceUrl is required')
    assert.ok(entry.sourceConsultedAt && entry.sourceConsultedAt.trim().length > 0, 'sourceConsultedAt is required')
    if (entry.sourceVerified) {
      assert.match(entry.sourceUrl, /^https:\/\//u, 'a verified source must be an https URL')
    }
    if (entry.role === 'subprocessor' && entry.sourceVerified) {
      assert.ok(entry.dpaReference !== undefined, 'subprocessor entries must state a DPA reference or null')
    }
  }
})

test('the subprocessor document references every registry vendor by legal name', async () => {
  const raw = await readFile(resolve(featureRoot, 'content/subprocessors.json'), 'utf8')
  const registry = JSON.parse(raw) as { entries: Array<{ legalName: string }> }
  const artifacts = (await loadDraftArtifacts()).filter(item => item.document === 'subprocessors')
  for (const artifact of artifacts) {
    for (const entry of registry.entries) {
      const firstName = entry.legalName.split(/[;(]/u)[0]?.trim()
      if (!firstName || firstName === 'Not verified') {
        continue
      }
      assert.ok(
        artifact.content.includes(firstName),
        `${artifact.locale} subprocessors document is missing registry vendor ${firstName}`,
      )
    }
  }
})
