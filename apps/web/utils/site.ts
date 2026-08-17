import { DEFAULT_LOCALE, LOCALES, localePath } from '~/utils/locale'

/**
 * Costruisce l'URL canonico assoluto di un percorso del sito pubblico.
 *
 * Serve per i tag `canonical`, `alternate` e `og:url`: su hosting statico
 * l'URL del sito è noto solo al momento della build, quindi arriva da
 * `runtimeConfig.public.siteUrl`.
 *
 * L'indirizzo prodotto termina sempre con `/`. Ogni rotta pre-renderizzata è
 * una directory con dentro `index.html` e Cloudflare Pages reindirizza la forma
 * senza slash finale su quella con lo slash: dichiarare come canonica una
 * pagina che risponde 308 significa dichiarare l'indirizzo sbagliato.
 *
 * L'eventuale ancora viene scartata: `#pricing` è una posizione dentro la
 * pagina, non una pagina diversa.
 *
 * @param path percorso relativo, con o senza slash iniziale
 * @param siteUrl origin del sito, con o senza slash finale
 */
export function canonicalUrl(path: string, siteUrl: string): string {
  const base = siteUrl.replace(/\/+$/, '')
  const withoutHash = path.split('#')[0] ?? ''
  const segments = withoutHash.split('/').filter(Boolean)

  return segments.length === 0 ? `${base}/` : `${base}/${segments.join('/')}/`
}

/**
 * Indirizzo assoluto di un file statico — la card social, un'immagine.
 *
 * Non è `canonicalUrl`: quella chiude sempre con lo slash perché una rotta
 * pre-renderizzata è una directory, mentre `/social-card.png/` è un indirizzo
 * che non esiste. Un crawler che lo chiede riceve un 404 e mostra
 * un'anteprima muta.
 */
export function assetUrl(path: string, siteUrl: string): string {
  return `${siteUrl.replace(/\/+$/, '')}/${path.replace(/^\/+/, '')}`
}

export interface AlternateLink {
  hreflang: string
  href: string
}

/**
 * Indirizzi `hreflang` di una pagina in tutte le lingue, più `x-default`.
 *
 * Senza questi tag le cinque versioni della stessa pagina competono fra loro
 * nei motori di ricerca invece di dichiararsi traduzioni l'una dell'altra
 * (SPEC §8-bis). `x-default` punta all'inglese: è la lingua predefinita, quella
 * che il rilevamento sceglie quando nessuna preferenza corrisponde.
 *
 * @param path percorso **senza** prefisso di lingua, nella forma di `content/`
 * @param siteUrl origin del sito
 */
export function alternateLinks(path: string, siteUrl: string): readonly AlternateLink[] {
  return [
    ...LOCALES.map(locale => ({
      hreflang: locale.htmlLang,
      href: canonicalUrl(localePath(path, locale.code), siteUrl),
    })),
    { hreflang: 'x-default', href: canonicalUrl(localePath(path, DEFAULT_LOCALE), siteUrl) },
  ]
}

/**
 * Sostituisce i segnaposto `{nome}` di una stringa di traduzione.
 *
 * Le poche etichette che contengono un valore variabile — «Photo of {name}» —
 * restano così frasi intere nel file di lingua invece di essere spezzate e
 * ricomposte nel componente, dove l'ordine delle parti sarebbe quello
 * dell'inglese per tutte e cinque le lingue.
 */
export function interpolate(template: string, values: Record<string, string>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) => values[key] ?? match)
}
