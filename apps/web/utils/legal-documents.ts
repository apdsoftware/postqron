/**
 * Identità dei documenti legali, separata dal loro contenuto.
 *
 * `utils/legal.ts` incorpora i Markdown con `?raw` e li converte con `marked`:
 * è codice che solo Vite sa caricare. L'elenco degli identificatori serve però
 * anche fuori dall'applicazione — alla sitemap, che si genera in Node durante
 * la build (`modules/seo-files.ts`) — e da lì un `?raw` non si risolve.
 *
 * Restano quindi due file: qui i nomi, là i testi. `utils/legal.ts` li
 * riesporta, così chi lavora sui documenti continua a importare da un posto solo.
 */

export const LEGAL_DOCUMENT_IDS = [
  'terms-of-service',
  'privacy-policy',
  'cookie-policy',
  'acceptable-use-policy',
] as const

export type LegalDocumentId = (typeof LEGAL_DOCUMENT_IDS)[number]

export function isLegalDocumentId(value: string): value is LegalDocumentId {
  return (LEGAL_DOCUMENT_IDS as readonly string[]).includes(value)
}
