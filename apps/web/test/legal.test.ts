import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import type { LegalDocumentId } from '~/utils/legal-documents'
import type { LocaleCode } from '~/utils/locale'
import { siteContent } from '~/content'
import { DEFAULT_LOCALE, LOCALE_CODES } from '~/utils/locale'
import { LEGAL_DOCUMENT_IDS } from '~/utils/legal-documents'
import { legalDocument } from '~/utils/legal'

const LEGAL_ROOT = resolve(process.cwd(), '../../legal')
const OPEN_PLACEHOLDER = /\[\[DA CONFERMARE:[\s\S]*?\]\]/

/** Le quattro lingue di arrivo: l'inglese è l'originale, non una traduzione. */
const TRANSLATED_LOCALES = LOCALE_CODES.filter(locale => locale !== DEFAULT_LOCALE)

/** Ogni combinazione lingua × documento, cioè i venti file di `legal/`. */
const EVERY_DOCUMENT: [LocaleCode, LegalDocumentId][] = LOCALE_CODES.flatMap(
  locale => LEGAL_DOCUMENT_IDS.map((id): [LocaleCode, LegalDocumentId] => [locale, id]),
)

function documentPath(locale: string, id: string): string {
  return resolve(LEGAL_ROOT, locale, `${id}.md`)
}

function source(locale: string, id: string): string {
  return readFileSync(documentPath(locale, id), 'utf8')
}

/** Front matter come coppie chiave/valore, con la stessa lettura di `utils/legal.ts`. */
function frontMatter(locale: string, id: string): Record<string, string> {
  const match = source(locale, id).match(/^---\n([\s\S]*?)\n---\n/)
  if (!match) throw new Error(`Front matter non valido in legal/${locale}/${id}.md`)

  return Object.fromEntries(
    match[1]!.split('\n').map((line) => {
      const separator = line.indexOf(':')
      return [line.slice(0, separator).trim(), line.slice(separator + 1).trim()]
    }),
  )
}

/**
 * Titoli numerati, nella forma «livello + numero»: `## 4` , `### 4.1`.
 *
 * È la struttura che i rinvii incrociati indirizzano (`§4.3`), ed è l'unica
 * parte di un documento che una traduzione non può muovere.
 */
function numbering(locale: string, id: string): string[] {
  return [...source(locale, id).matchAll(/^(#{1,3}) (\d+(?:\.\d+)?)\.? /gm)]
    .map(([, level, number]) => `${level} ${number}`)
}

/** Bersagli dei collegamenti relativi fra documenti, nell'ordine in cui compaiono. */
function crossLinks(locale: string, id: string): string[] {
  return [...source(locale, id).matchAll(/\]\(([a-z-]+\.md)\)/g)].map(([, target]) => target!)
}

describe('documenti legali pubblicati', () => {
  // Le versioni divergono: i Termini sono alla 1.2.0 e la Privacy alla 1.1.0,
  // gli altri due restano alla 1.0.0. Un'attesa unica le avrebbe allineate a
  // forza, cioè avrebbe nascosto proprio il fatto che questo test esiste per
  // sorvegliare — la prova del consenso registra quale versione l'utente ha
  // accettato, documento per documento (R46).
  const VERSIONI: Record<string, string> = {
    'terms-of-service': '1.2.0',
    'privacy-policy': '1.1.0',
    'cookie-policy': '1.0.0',
    'acceptable-use-policy': '1.0.0',
  }

  // La data segue la versione, documento per documento, per la stessa ragione:
  // è una proprietà della **versione**, non del lancio, e il consenso registra
  // entrambe. Tenerla unica qui l'aveva già resa falsa — due documenti sono
  // passati al 18 agosto e il test pretendeva ancora il 17 per tutti e quattro.
  const DATE: Record<string, string> = {
    'terms-of-service': '2026-08-18',
    'privacy-policy': '2026-08-18',
    'cookie-policy': '2026-08-17',
    'acceptable-use-policy': '2026-08-17',
  }

  it.each(LEGAL_DOCUMENT_IDS)('%s espone versione e data approvate', (id) => {
    const document = legalDocument(id, DEFAULT_LOCALE)

    expect(document.version).toBe(VERSIONI[id])
    expect(document.effectiveDate).toBe(DATE[id])
    expect(document.language).toBe('en')
    expect(document.html).not.toBe('')
  })

  it.each(EVERY_DOCUMENT)('%s/%s non contiene segnaposto aperti', (locale, id) => {
    expect(source(locale, id)).not.toMatch(OPEN_PLACEHOLDER)
  })

  it('riscrive i collegamenti fra documenti nella lingua della rotta', () => {
    const document = legalDocument('terms-of-service', 'it')

    expect(document.html).toContain('href="/it/legal/acceptable-use-policy/"')
    expect(document.html).toContain('href="/it/legal/privacy-policy/"')
  })
})

/**
 * Le traduzioni (#447), verificate sui file e non sul risultato reso.
 *
 * Il difetto peggiore qui è una traduzione con la versione sbagliata: il
 * registro del consenso dice «accettata la 1.0.0 in tedesco» di un testo che
 * traduce la 1.2.0, e nessuno se ne accorge leggendo la pagina. Un test che
 * copra il solo inglese non lo vedrebbe mai, perché l'inglese è giusto.
 */
describe('traduzioni dei documenti legali', () => {
  it.each(EVERY_DOCUMENT)('%s/%s dichiara documento, lingua, versione e data', (locale, id) => {
    const metadata = frontMatter(locale, id)
    const originale = frontMatter(DEFAULT_LOCALE, id)

    expect(metadata.document).toBe(id)
    expect(metadata.language).toBe(locale)
    expect(metadata.version).toBe(originale.version)
    expect(metadata.effective_date).toBe(originale.effective_date)
  })

  it.each(EVERY_DOCUMENT)('%s/%s conserva la numerazione dei paragrafi', (locale, id) => {
    expect(numbering(locale, id)).toEqual(numbering(DEFAULT_LOCALE, id))
  })

  it.each(EVERY_DOCUMENT)('%s/%s conserva i collegamenti fra documenti', (locale, id) => {
    // I nomi dei file sono identificativi di documento, non testo: sono la rotta
    // pubblica, e `utils/legal.ts` li riscrive nella lingua corrente. Tradurne
    // uno spezzerebbe il collegamento in silenzio.
    expect(crossLinks(locale, id)).toEqual(crossLinks(DEFAULT_LOCALE, id))
  })

  it.each(EVERY_DOCUMENT)('%s/%s non traduce dati societari e fornitori', (locale, id) => {
    const text = source(locale, id)
    const originale = source(DEFAULT_LOCALE, id)

    expect(text).toContain('Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224')
    // «Merchant of Record» è un termine tecnico: determina chi risponde di
    // rimborsi, imposte e fatturazione. Compare tante volte quanto nell'originale.
    expect(text.split('Merchant of Record').length)
      .toBe(originale.split('Merchant of Record').length)
    for (const address of originale.match(/[a-z]+@postqron\.com/g) ?? []) {
      expect(text).toContain(address)
    }
  })

  it.each(TRANSLATED_LOCALES)('%s dichiara lo stato di revisione di ogni documento', (locale) => {
    for (const id of LEGAL_DOCUMENT_IDS) {
      expect(frontMatter(locale, id).status).toMatch(/^(pending-review|approved)$/)
    }
  })

  it.each(LEGAL_DOCUMENT_IDS)('%s: la lingua sorgente non porta uno stato', (id) => {
    // L'inglese non ha una revisione della traduzione da attendere: è il testo
    // che le altre traducono.
    expect(frontMatter(DEFAULT_LOCALE, id)).not.toHaveProperty('status')
  })

  it.each(EVERY_DOCUMENT)('%s/%s mostra la traduzione solo se approvata', (locale, id) => {
    const approvato = locale === DEFAULT_LOCALE || frontMatter(locale, id).status === 'approved'
    const document = legalDocument(id, locale)

    expect(document.language).toBe(approvato ? locale : DEFAULT_LOCALE)
    // Versione e data restano quelle del documento, in qualunque lingua sia
    // finito per essere mostrato: sono ciò che il consenso registra.
    expect(document.version).toBe(frontMatter(DEFAULT_LOCALE, id).version)
  })
})

/**
 * La codifica, verificata **sui byte**.
 *
 * Questi documenti contengono trattini lunghi, virgolette tipografiche e
 * accenti. Un passaggio con uno strumento che non dichiara UTF-8 li
 * doppia-codifica, ed è già successo su questo repository: la verifica di allora
 * cercava la resa Windows-1252 in una stringa già decodificata, trovava zero e
 * lasciava passare il difetto. Cercare in una stringa decodificata non può
 * funzionare — la decodifica è proprio il passaggio che nasconde il problema.
 */
describe('codifica dei documenti legali', () => {
  /**
   * Le tre firme, in byte, di un testo doppia-codificato.
   *
   * Ogni carattere non ASCII di questi documenti sta o nel supplemento Latin-1
   * (lettere accentate, `§`, `«`, `»`, spazio unificatore: primo byte `C2` o
   * `C3`) o nella punteggiatura generale (trattino lungo, virgolette
   * tipografiche: primo byte `E2`). Passandoli per Windows-1252 quel primo byte
   * diventa un carattere a sé — rispettivamente `Â`, `Ã` e `â` — e ricodificarlo
   * lascia queste sequenze. `Â` e `Ã` non esistono in nessuna delle cinque
   * lingue; `â` sì, in francese, ma mai seguito da `€`, che è quel che diventa
   * il byte `80` di ogni trattino e di ogni virgoletta.
   */
  const DOPPIA_CODIFICA: [string, Buffer][] = [
    ['Â', Buffer.from([0xC3, 0x82])],
    ['Ã', Buffer.from([0xC3, 0x83])],
    ['â€', Buffer.from([0xC3, 0xA2, 0xE2, 0x82, 0xAC])],
  ]
  /** Il trattino lungo in UTF-8, presente in ogni documento: il controllo positivo. */
  const TRATTINO_LUNGO = Buffer.from([0xE2, 0x80, 0x94])

  it.each(EVERY_DOCUMENT)('%s/%s è UTF-8 valido e non doppia-codificato', (locale, id) => {
    const bytes = readFileSync(documentPath(locale, id))

    // Si legge il file come byte, non come stringa: decodificarlo è proprio il
    // passaggio che nasconde il difetto. Se un byte non appartiene a una
    // sequenza UTF-8 valida, decodificarlo produce U+FFFD e ricodificarlo dà
    // byte diversi — il confronto lo vede.
    expect(bytes.equals(Buffer.from(bytes.toString('utf8'), 'utf8'))).toBe(true)
    for (const [nome, sequenza] of DOPPIA_CODIFICA) {
      expect({ [nome]: bytes.includes(sequenza) }).toEqual({ [nome]: false })
    }
    // Senza questo il controllo qui sopra passerebbe anche su un file di solo
    // ASCII, cioè su una traduzione che ha già perso i suoi caratteri.
    expect(bytes.includes(TRATTINO_LUNGO)).toBe(true)
  })
})

describe('impegni non approvati', () => {
  it.each(LOCALE_CODES)('%s non promette trial, pagina di stato o canale video', (locale) => {
    const content = siteContent[locale]
    const footerLinks = content.nav.footer.flatMap(group => group.items.map(item => item.to))
    const serialized = JSON.stringify(content).toLowerCase()

    expect(content.hero.note).toBeUndefined()
    expect(content.hero.video).toBeUndefined()
    expect(footerLinks).not.toContain('/#stats')
    expect(serialized).not.toContain('youtube.com/@postqron')
  })
})
