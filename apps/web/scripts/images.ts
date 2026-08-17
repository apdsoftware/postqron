/**
 * Genera le varianti pubbliche delle fotografie del sito.
 *
 * Legge `utils/images.ts`, prende i sorgenti da `images/` e scrive in
 * `public/img/gen/` una variante per ogni combinazione di larghezza e formato
 * (AVIF, WebP, ripiego JPEG o PNG). Gira prima di `nuxt generate` e prima di
 * `nuxt dev`: le varianti sono artefatti, non stanno in Git.
 *
 * Perché non `@nuxt/image`. Quel modulo risolve un problema che qui non c'è:
 * trasformare a runtime immagini che arrivano da un CMS o da un dominio terzo.
 * Le nostre sono sei file fermi in repository, e il sito è statico su Cloudflare
 * Pages (SPEC §2) — non esiste un processo che possa servire `/_ipx/`. In quel
 * modo `@nuxt/image` aggiungerebbe un componente da idratare e circa 20 KB di
 * JavaScript al bundle di una pagina che deve stare sotto i 95 di Lighthouse
 * proprio perché di JavaScript ne ha già troppo (R53-bis). Questo script fa la
 * stessa cosa in build e non lascia nulla a runtime: il markup che ne esce è un
 * `<picture>` con dentro un `<img>`, che è ciò che il browser saprebbe fare da
 * solo.
 *
 * Lo script fallisce — e con lui la build — se un sorgente non ha le dimensioni
 * dichiarate nel registro o se una variante sfonda il tetto di peso. È il
 * controllo che serve: una fotografia sostituita senza guardare la bilancia è
 * il modo normale in cui un sito torna lento.
 *
 * Uso: `pnpm --filter @postqron/web run images`
 */

import { mkdir, readdir, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import process from 'node:process'
import sharp, { type Sharp } from 'sharp'

import {
  MODERN_FORMATS,
  RASTER_IMAGES,
  RASTER_IMAGE_NAMES,
  type ImageFormat,
  type RasterImageName,
} from '../utils/images.ts'

const ROOT = dirname(dirname(fileURLToPath(import.meta.url)))
const SOURCE_DIR = join(ROOT, 'images')
const OUTPUT_DIR = join(ROOT, 'public', 'img', 'gen')

/**
 * Le catture dell'interfaccia hanno bordi netti e testo: la stessa qualità che
 * su una fotografia è invisibile, lì produce aloni intorno alle lettere. Le due
 * tabelle sono indicizzate dal formato del ripiego, che è anche il modo più
 * onesto di dire «questo file è una fotografia» o «questo è uno screenshot».
 */
const QUALITY = {
  jpg: { avif: 48, webp: 76, fallback: 76 },
  png: { avif: 68, webp: 90, fallback: 100 },
} as const

interface Written {
  name: RasterImageName
  width: number
  format: ImageFormat
  bytes: number
  path: string
}

async function encode(
  pipeline: Sharp,
  format: ImageFormat,
  quality: typeof QUALITY[keyof typeof QUALITY],
): Promise<Buffer> {
  switch (format) {
    case 'avif':
      return pipeline.avif({ quality: quality.avif, effort: 6 }).toBuffer()
    case 'webp':
      return pipeline.webp({ quality: quality.webp, effort: 6 }).toBuffer()
    case 'png':
      // `palette` porta il PNG a 8 bit indicizzati: su una cattura di interfaccia,
      // che di colori ne usa poche decine, è una riduzione senza perdita visibile.
      return pipeline.png({ palette: true, quality: quality.fallback, effort: 10 }).toBuffer()
    case 'jpg':
      return pipeline.jpeg({ quality: quality.fallback, mozjpeg: true, progressive: true }).toBuffer()
  }
}

async function main() {
  const problems: string[] = []
  const written: Written[] = []

  // La directory si ricrea da zero: una variante rimasta da un registro
  // precedente verrebbe pubblicata senza che nessuno la referenzi.
  await rm(OUTPUT_DIR, { recursive: true, force: true })

  const known = new Set<string>()

  for (const name of RASTER_IMAGE_NAMES) {
    const image = RASTER_IMAGES[name]
    const source = join(SOURCE_DIR, `${name}.${image.fallback}`)
    known.add(`${name}.${image.fallback}`)

    const meta = await sharp(source).metadata()
    if (meta.width !== image.width || meta.height !== image.height) {
      problems.push(
        `${name}: il registro dichiara ${image.width}×${image.height}, `
        + `il file è ${meta.width}×${meta.height} — allinea utils/images.ts`,
      )
      continue
    }

    const widest = image.widths[image.widths.length - 1]
    if (widest !== undefined && widest > image.width) {
      problems.push(`${name}: la larghezza ${widest} supera il nativo ${image.width}, sarebbe un ingrandimento`)
      continue
    }

    await mkdir(join(OUTPUT_DIR, dirname(name)), { recursive: true })

    for (const width of image.widths) {
      for (const format of [...MODERN_FORMATS, image.fallback] as const) {
        // Una pipeline nuova per variante: sharp consuma l'istanza a ogni
        // `toBuffer()` e riusarla darebbe risultati dipendenti dall'ordine.
        const buffer = await encode(
          sharp(source).resize({ width, withoutEnlargement: true }),
          format,
          QUALITY[image.fallback],
        )

        const path = join(OUTPUT_DIR, `${name}-${width}.${format}`)
        await writeFile(path, buffer)
        written.push({ name, width, format, bytes: buffer.length, path })

        if (buffer.length > image.maxBytes) {
          problems.push(
            `${name}-${width}.${format}: ${kb(buffer.length)} oltre il tetto di `
            + `${kb(image.maxBytes)} dichiarato nel registro`,
          )
        }
      }
    }
  }

  // Un sorgente che non compare nel registro non viene generato, quindi non
  // viene pubblicato: sarebbe un file dimenticato in repository e nient'altro.
  for (const orphan of await sources(SOURCE_DIR)) {
    if (!known.has(orphan)) problems.push(`images/${orphan}: nessuna voce nel registro utils/images.ts`)
  }

  report(written)

  if (problems.length > 0) {
    console.error('')
    for (const problem of problems) console.error(`  ✗ ${problem}`)
    console.error('')
    process.exit(1)
  }
}

/** Elenco ricorsivo dei sorgenti, con percorso relativo a `images/`. */
async function sources(dir: string, prefix = ''): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true })
  const found: string[] = []
  for (const entry of entries) {
    if (entry.isDirectory()) found.push(...await sources(join(dir, entry.name), `${prefix}${entry.name}/`))
    else found.push(`${prefix}${entry.name}`)
  }
  return found
}

function kb(bytes: number): string {
  return `${(bytes / 1024).toFixed(1)} KB`
}

function report(written: Written[]) {
  for (const name of RASTER_IMAGE_NAMES) {
    const ours = written.filter(w => w.name === name)
    if (ours.length === 0) continue
    const widest = Math.max(...ours.map(w => w.width))
    const line = [...MODERN_FORMATS, RASTER_IMAGES[name].fallback]
      .map((format) => {
        const file = ours.find(w => w.width === widest && w.format === format)
        return file ? `${format} ${kb(file.bytes)}` : ''
      })
      .filter(Boolean)
      .join('  ')
    console.info(`  ${name.padEnd(22)} ${String(widest).padStart(5)}px   ${line}`)
  }
  const total = written.reduce((sum, w) => sum + w.bytes, 0)
  console.info(`  ${'—'.repeat(22)} ${written.length} varianti, ${kb(total)} in tutto`)
}

await main()
