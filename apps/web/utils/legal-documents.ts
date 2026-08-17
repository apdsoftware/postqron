/**
 * Identità dei documenti legali, separata dal loro contenuto.
 *
 * `utils/legal.ts` incorpora i Markdown con `?raw` e li converte con `marked`:
 * è codice che solo Vite sa caricare. L'elenco degli identificatori serve però
 * anche fuori dall'applicazione — alla sitemap, che si genera in Node durante
 * la build (`modules/seo-files.ts`) — e da lì un `?raw` non si risolve.
 *
 * Restano quindi due file: qui i nomi, là i testi.
 *
 * La separazione ha un secondo effetto, ed è quello che la rende vincolante:
 * `definePageMeta` viene estratta in fase di build e finisce nel manifesto
 * delle rotte, che sta nel bundle d'ingresso — quello che scarica *ogni*
 * pagina, home compresa. Una `validate` che chiama una funzione di
 * `utils/legal.ts` ci trascina dentro l'intero modulo, e con lui `marked` e i
 * quattro Markdown incorporati. **La guardia della rotta legale deve quindi
 * importare da qui, non da `utils/legal.ts`.**
 */

export const LEGAL_DOCUMENT_IDS = [
  'terms-of-service',
  'privacy-policy',
  'cookie-policy',
  'acceptable-use-policy',
] as const

export type LegalDocumentId = (typeof LEGAL_DOCUMENT_IDS)[number]

/** Accetta `unknown` perché il chiamante è un parametro di rotta, che può essere un array. */
export function isLegalDocumentId(value: unknown): value is LegalDocumentId {
  return typeof value === 'string' && (LEGAL_DOCUMENT_IDS as readonly string[]).includes(value)
}
