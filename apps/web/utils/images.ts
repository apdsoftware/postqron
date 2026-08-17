/**
 * Registro delle immagini raster del sito pubblico.
 *
 * È l'unica fonte di verità sulle fotografie: quanto sono grandi davvero, a
 * quali larghezze vanno servite e quanto possono pesare. Tre consumatori
 * diversi leggono queste stesse righe:
 *
 * 1. `scripts/images.ts` genera le varianti in `public/img/gen/`, e fallisce se
 *    il file sorgente non ha le dimensioni dichiarate qui o se una variante
 *    sfonda il tetto di peso;
 * 2. `ResponsiveImage.vue` compone `srcset` e gli attributi `width`/`height`;
 * 3. `test/images.test.ts` verifica che ogni immagine citata dai contenuti
 *    esista nel registro e che ogni voce del registro sia usata.
 *
 * La duplicazione è la cosa da evitare: finché le dimensioni stavano nei
 * contenuti (`imageWidth`/`imageHeight` di `Showcase`) esistevano in due posti,
 * e la copia che il browser usa per riservare lo spazio non era quella che il
 * file aveva davvero.
 *
 * I sorgenti stanno in `apps/web/images/` e **non** vengono pubblicati: nel
 * sito finisce solo ciò che `scripts/images.ts` produce. Un originale servito
 * per sbaglio è peso che nessun browser scarica ma che il deploy trasferisce.
 */

/** Formato del ripiego, per i browser che non leggono né AVIF né WebP. */
export type RasterFallback = 'jpg' | 'png'

export interface RasterImage {
  /** Larghezza nativa del sorgente, in pixel. */
  readonly width: number
  /** Altezza nativa del sorgente, in pixel. */
  readonly height: number
  /**
   * Larghezze da generare, crescenti. Nessuna può superare `width`: ingrandire
   * un originale aggiunge byte e non aggiunge dettaglio — è esattamente il
   * difetto da cui partiva questa issue, con l'hero portato a 1200px da un
   * originale di 1065.
   */
  readonly widths: readonly number[]
  readonly fallback: RasterFallback
  /**
   * Tetto di peso per singolo file generato, in byte. Vale su tutti i formati:
   * quello che lo tocca è sempre il ripiego alla larghezza massima, quindi è lì
   * che si accorge di una sostituzione fatta senza guardare la bilancia.
   */
  readonly maxBytes: number
  /** Descrizione del contenuto, per chi legge il registro. */
  readonly note: string
}

export const RASTER_IMAGES = {
  'hero': {
    // Il ritaglio esagonale copre metà della foto: la parte utile è meno di
    // quel che il file suggerisce, ma il box è largo 55,5vw fino a 1920px.
    width: 1065,
    height: 955,
    // Cinque candidate, e non quattro come per le altre: è l'unica immagine il
    // cui peso entra nell'LCP, quindi vale la pena avvicinare la variante
    // scelta al fabbisogno reale. Un telefono da 390px con DPR 2 ne chiede 780
    // e con 860 in elenco ne scaricherebbe il 10% in più; a DPR 1,75 — quello
    // che Lighthouse emula — ne chiede 721 e il divario arrivava al 19%.
    widths: [420, 640, 780, 900, 1065],
    fallback: 'jpg',
    maxBytes: 80_000,
    note: 'Fotografia dell\'hero, elemento LCP della home.',
  },
  'blog/1': {
    width: 800,
    height: 533,
    widths: [400, 560, 800],
    fallback: 'jpg',
    maxBytes: 55_000,
    note: 'Copertina del primo articolo.',
  },
  'blog/2': {
    width: 800,
    height: 529,
    widths: [400, 560, 800],
    fallback: 'jpg',
    maxBytes: 55_000,
    note: 'Copertina del secondo articolo.',
  },
  'blog/3': {
    width: 800,
    height: 533,
    widths: [400, 560, 800],
    fallback: 'jpg',
    maxBytes: 55_000,
    note: 'Copertina del terzo articolo.',
  },
  // Una sola larghezza, e non è una svista. Sono catture dell'interfaccia: la
  // colonna che le ospita non le mostra mai più larghe del nativo, e sotto la
  // larghezza nativa un telefono con DPR 2 chiederebbe comunque più pixel di
  // quanti il file ne abbia. Una seconda variante sarebbe un file in più che
  // nessun `sizes` sceglierebbe mai.
  'screenshots/jobs': {
    width: 593,
    height: 467,
    widths: [593],
    fallback: 'png',
    maxBytes: 15_000,
    note: 'Cattura dell\'elenco dei job.',
  },
  'screenshots/metrics': {
    width: 605,
    height: 375,
    widths: [605],
    fallback: 'png',
    maxBytes: 15_000,
    note: 'Cattura del pannello delle metriche.',
  },
} as const satisfies Record<string, RasterImage>

export type RasterImageName = keyof typeof RASTER_IMAGES

export const RASTER_IMAGE_NAMES = Object.keys(RASTER_IMAGES) as RasterImageName[]

/** Directory pubblica delle varianti generate. Non contiene nulla di scritto a mano. */
export const GENERATED_PREFIX = '/img/gen'

/** Estensioni prodotte, dalla più efficiente alla più compatibile. */
export const MODERN_FORMATS = ['avif', 'webp'] as const

export type ImageFormat = typeof MODERN_FORMATS[number] | RasterFallback

/** Indirizzo pubblico di una singola variante. */
export function variantUrl(name: RasterImageName, width: number, format: ImageFormat): string {
  return `${GENERATED_PREFIX}/${name}-${width}.${format}`
}

/**
 * `srcset` di un formato, o `undefined` quando c'è una sola larghezza: con una
 * candidata sola il `srcset` non aggiunge nulla e `src` basta.
 */
export function srcSet(name: RasterImageName, format: ImageFormat): string | undefined {
  const image = RASTER_IMAGES[name]
  if (image.widths.length < 2) return undefined
  return image.widths.map(width => `${variantUrl(name, width, format)} ${width}w`).join(', ')
}

/**
 * Indirizzo della variante più larga in un formato. È ciò che finisce in `src`
 * quando `srcset` non c'è, ed è il candidato di partenza quando c'è: chi ci
 * arriva senza leggere `srcset` è un browser vecchio, e per lui la nitidezza
 * conta più dei byte.
 */
export function widestUrl(name: RasterImageName, format: ImageFormat): string {
  const image = RASTER_IMAGES[name]
  const widest = image.widths[image.widths.length - 1] ?? image.width
  return variantUrl(name, widest, format)
}

/** Indirizzo del ripiego, nel formato compatibile dell'immagine. */
export function fallbackUrl(name: RasterImageName): string {
  return widestUrl(name, RASTER_IMAGES[name].fallback)
}
