// Questo modulo porta con sé `marked` e i venti Markdown incorporati — i quattro
// documenti per ciascuna delle cinque lingue. Va importato **solo** da chi rende
// davvero un documento legale, cioè dal componente della pagina, che è un chunk
// pigro. Il predicato sugli identificatori sta apposta in
// `utils/legal-documents.ts`: usare quello nelle guardie di rotta è ciò che
// tiene questo peso fuori dal bundle d'ingresso di tutte le altre pagine.
//
// Gli import sono venti e statici perché la rotta è una sola per tutte le
// lingue (`pages/[locale]/legal/[document].vue`): qualunque suddivisione per
// lingua produrrebbe comunque un unico chunk, e renderla asincrona costringerebbe
// la pagina a un `await` per mostrare un testo che il prerender ha già scritto
// nell'HTML.
import type { LegalDocumentId } from '~/utils/legal-documents'
import type { LocaleCode } from '~/utils/locale'
import { Renderer, marked } from 'marked'
import { DEFAULT_LOCALE } from '~/utils/locale'
import deAcceptableUseSource from '../../../legal/de/acceptable-use-policy.md?raw'
import deCookieSource from '../../../legal/de/cookie-policy.md?raw'
import dePrivacySource from '../../../legal/de/privacy-policy.md?raw'
import deTermsSource from '../../../legal/de/terms-of-service.md?raw'
import enAcceptableUseSource from '../../../legal/en/acceptable-use-policy.md?raw'
import enCookieSource from '../../../legal/en/cookie-policy.md?raw'
import enPrivacySource from '../../../legal/en/privacy-policy.md?raw'
import enTermsSource from '../../../legal/en/terms-of-service.md?raw'
import esAcceptableUseSource from '../../../legal/es/acceptable-use-policy.md?raw'
import esCookieSource from '../../../legal/es/cookie-policy.md?raw'
import esPrivacySource from '../../../legal/es/privacy-policy.md?raw'
import esTermsSource from '../../../legal/es/terms-of-service.md?raw'
import frAcceptableUseSource from '../../../legal/fr/acceptable-use-policy.md?raw'
import frCookieSource from '../../../legal/fr/cookie-policy.md?raw'
import frPrivacySource from '../../../legal/fr/privacy-policy.md?raw'
import frTermsSource from '../../../legal/fr/terms-of-service.md?raw'
import itAcceptableUseSource from '../../../legal/it/acceptable-use-policy.md?raw'
import itCookieSource from '../../../legal/it/cookie-policy.md?raw'
import itPrivacySource from '../../../legal/it/privacy-policy.md?raw'
import itTermsSource from '../../../legal/it/terms-of-service.md?raw'

export interface LegalDocument {
  id: LegalDocumentId
  title: string
  version: string
  effectiveDate: string
  /**
   * Lingua del testo **effettivamente mostrato**, che non coincide con quella
   * della rotta finché la traduzione non è approvata. La pagina confronta i due
   * valori per decidere se mostrare l'avviso di ripiego, e `<article lang>` usa
   * questo: dichiarare `it` su un testo inglese direbbe una cosa falsa alla
   * sintesi vocale e al motore di ricerca.
   */
  language: LocaleCode
  html: string
}

/**
 * Valore di `status` che rende pubblicabile una traduzione (`legal/README.md`).
 *
 * L'inglese è la lingua sorgente e non porta il campo: non c'è una revisione da
 * attendere, è il testo che le altre traducono. Una traduzione senza `status:
 * approved` **non viene mostrata**, e al suo posto compare l'originale inglese:
 * la prova del consenso registra versione e lingua di ciò che l'utente ha letto
 * (R46), quindi pubblicare una traduzione non ancora rivista significherebbe
 * raccogliere un consenso su un testo di cui nessuno risponde.
 */
const APPROVED = 'approved'

const sources: Record<LocaleCode, Record<LegalDocumentId, string>> = {
  en: {
    'terms-of-service': enTermsSource,
    'privacy-policy': enPrivacySource,
    'cookie-policy': enCookieSource,
    'acceptable-use-policy': enAcceptableUseSource,
  },
  it: {
    'terms-of-service': itTermsSource,
    'privacy-policy': itPrivacySource,
    'cookie-policy': itCookieSource,
    'acceptable-use-policy': itAcceptableUseSource,
  },
  es: {
    'terms-of-service': esTermsSource,
    'privacy-policy': esPrivacySource,
    'cookie-policy': esCookieSource,
    'acceptable-use-policy': esAcceptableUseSource,
  },
  de: {
    'terms-of-service': deTermsSource,
    'privacy-policy': dePrivacySource,
    'cookie-policy': deCookieSource,
    'acceptable-use-policy': deAcceptableUseSource,
  },
  fr: {
    'terms-of-service': frTermsSource,
    'privacy-policy': frPrivacySource,
    'cookie-policy': frCookieSource,
    'acceptable-use-policy': frAcceptableUseSource,
  },
}

/** Converte i link relativi fra documenti nelle rotte pubbliche della lingua corrente. */
function legalRenderer(locale: string): Renderer {
  const renderer = new Renderer()

  renderer.link = function ({ href, title, tokens }) {
    const target = href.replace(/^\.\//, '').replace(/\.md$/, '')
    const localizedHref = isLegalDocumentId(target) ? `/${locale}/legal/${target}/` : href
    const titleAttribute = title ? ` title="${title}"` : ''

    return `<a href="${localizedHref}"${titleAttribute}>${this.parser.parseInline(tokens)}</a>`
  }

  return renderer
}

interface ParsedSource {
  metadata: Record<string, string>
  title: string
  body: string
}

/** Legge front matter, titolo e corpo senza modificare il testo giuridico. */
function parse(id: LegalDocumentId, language: LocaleCode): ParsedSource {
  const source = sources[language][id]
  const match = source.match(/^---\n([\s\S]*?)\n---\n+([\s\S]*)$/)
  if (!match) throw new Error(`Front matter non valido in legal/${language}/${id}.md`)

  const metadata = Object.fromEntries(
    match[1]!.split('\n').map((line) => {
      const separator = line.indexOf(':')
      return [line.slice(0, separator).trim(), line.slice(separator + 1).trim()]
    }),
  )
  const rest = match[2]!
  const heading = rest.match(/^# (.+)\n+/)
  if (!heading) throw new Error(`Titolo mancante in legal/${language}/${id}.md`)

  return { metadata, title: heading[1]!, body: rest.slice(heading[0].length) }
}

/**
 * Lingua in cui il documento può essere mostrato a chi legge in `locale`.
 *
 * È la lingua richiesta se la sua traduzione è approvata, altrimenti l'inglese.
 * Non esiste un terzo caso: non ripieghiamo su una lingua «vicina», perché
 * vicina non vuol dire nulla in un testo che ha valore legale.
 */
function publishedLanguage(id: LegalDocumentId, locale: LocaleCode): LocaleCode {
  if (locale === DEFAULT_LOCALE) return DEFAULT_LOCALE

  return parse(id, locale).metadata.status === APPROVED ? locale : DEFAULT_LOCALE
}

/**
 * Documento legale nella lingua richiesta, o nell'originale inglese quando la
 * traduzione non è ancora approvata.
 *
 * I file Markdown restano l'unica fonte: versione, data, titolo e contenuto
 * vengono incorporati direttamente da `legal/<lingua>/` durante la generazione.
 * Anche i collegamenti fra documenti restano nella lingua della rotta — è
 * `locale`, non la lingua del testo, a comporli: chi naviga in italiano continua
 * a muoversi fra le rotte italiane anche mentre legge un testo inglese.
 */
export function legalDocument(id: LegalDocumentId, locale: LocaleCode): LegalDocument {
  const language = publishedLanguage(id, locale)
  const { metadata, title, body } = parse(id, language)

  return {
    id,
    title,
    version: metadata.version ?? '',
    effectiveDate: metadata.effective_date ?? '',
    language,
    html: marked.parse(body, {
      async: false,
      renderer: legalRenderer(locale),
    }),
  }
}
