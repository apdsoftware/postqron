import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import { LEGAL_VERSIONS } from '#legal-versions'
import { COOKIE_POLICY_VERSION } from '~/utils/cookieConsent'
import { LEGAL_DOCUMENT_IDS } from '~/utils/legal-documents'

/**
 * Le versioni che il codice usa vengono dai documenti, non da una costante.
 *
 * `test/legal.test.ts` sorveglia i numeri approvati; qui si sorveglia il
 * percorso che li porta fino a chi li usa. È una cosa diversa e vale la pena
 * dirla separatamente: una costante ricopiata a mano resterebbe verde in quel
 * test e sbagliata in questo.
 */

const LEGAL_ROOT = resolve(process.cwd(), '../../legal/en')

function frontMatterVersion(id: string): string {
  const source = readFileSync(resolve(LEGAL_ROOT, `${id}.md`), 'utf8')
  const version = source.match(/^---\n[\s\S]*?^version:[ \t]*(\S+)[ \t]*$/m)?.[1]
  expect(version, `version mancante in legal/en/${id}.md`).toBeTruthy()

  return version!
}

describe('versioni dei documenti legali', () => {
  it.each(LEGAL_DOCUMENT_IDS)('%s porta nel codice la versione del proprio front matter', (id) => {
    expect(LEGAL_VERSIONS[id]).toBe(frontMatterVersion(id))
  })

  /*
   * Le quattro versioni non sono uguali, ed è proprio questo che rende
   * possibile l'errore di prendere il documento sbagliato: con quattro `1.0.0`
   * un cablaggio invertito passerebbe inosservato.
   */
  it('non allinea fra loro documenti che hanno storie diverse', () => {
    expect(new Set(Object.values(LEGAL_VERSIONS)).size).toBeGreaterThan(1)
  })

  it('lega il consenso ai cookie alla versione della cookie policy', () => {
    expect(COOKIE_POLICY_VERSION).toBe(frontMatterVersion('cookie-policy'))
    expect(COOKIE_POLICY_VERSION).not.toBe(LEGAL_VERSIONS['terms-of-service'])
  })
})
