/**
 * Costruisce l'URL canonico assoluto di un percorso del sito pubblico.
 *
 * Serve per i tag `canonical` e `og:url`: su hosting statico l'URL del sito è
 * noto solo al momento della build, quindi arriva da `runtimeConfig.public.siteUrl`.
 *
 * @param path percorso relativo, con o senza slash iniziale
 * @param siteUrl origin del sito, con o senza slash finale
 */
export function canonicalUrl(path: string, siteUrl: string): string {
  const base = siteUrl.replace(/\/+$/, '')
  const normalized = `/${path.replace(/^\/+/, '').replace(/\/+$/, '')}`
  return normalized === '/' ? `${base}/` : `${base}${normalized}`
}
