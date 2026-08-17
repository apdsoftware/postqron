import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import process from 'node:process'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ResponsiveImage from '~/components/ui/ResponsiveImage.vue'
import { siteContent } from '~/content'
import { publicPages } from '~/content/pages'
import { LOCALE_CODES } from '~/utils/locale'
import {
  MODERN_FORMATS,
  RASTER_IMAGES,
  RASTER_IMAGE_NAMES,
  fallbackUrl,
  srcSet,
  type RasterImageName,
} from '~/utils/images'

// Vitest gira con la radice di `apps/web` (vedi vitest.config.ts).
const APP_ROOT = process.cwd()

/** Tutti i nomi di immagine che i contenuti citano, in tutte e cinque le lingue. */
const referenced = new Set<RasterImageName>([
  ...LOCALE_CODES.flatMap(locale => [
    siteContent[locale].hero.image,
    ...siteContent[locale].articles.map(article => article.image),
  ]),
  ...LOCALE_CODES.flatMap(locale => publicPages[locale].features.showcases.map(showcase => showcase.image)),
])

describe('registro delle immagini', () => {
  it('dichiara le dimensioni native del file, non quelle di rendering', () => {
    // Il controllo vero — confronto con i pixel del sorgente — lo fa
    // `scripts/images.ts`, che i file li apre davvero. Qui resta la coerenza
    // interna: una larghezza generata più grande del nativo sarebbe un
    // ingrandimento, cioè byte in più e dettaglio uguale.
    for (const name of RASTER_IMAGE_NAMES) {
      const image = RASTER_IMAGES[name]
      expect(image.widths.length, `${name}: nessuna larghezza da generare`).toBeGreaterThan(0)
      expect(Math.max(...image.widths), `${name}: ingrandimento oltre il nativo`).toBeLessThanOrEqual(image.width)
      expect([...image.widths], `${name}: larghezze non crescenti`).toEqual([...image.widths].sort((a, b) => a - b))
    }
  })

  it('ha una voce per ogni immagine citata dai contenuti, e nessuna in più', () => {
    // Il verso «citata → esiste» lo garantisce già il tipo `RasterImageName`.
    // Quello che il compilatore non vede è il verso opposto: una voce che
    // nessuno usa resta a generare varianti che nessuno scarica.
    expect([...referenced].sort()).toEqual([...RASTER_IMAGE_NAMES].sort())
  })

  it('esiste il sorgente di ogni voce', () => {
    for (const name of RASTER_IMAGE_NAMES) {
      const source = join(APP_ROOT, 'images', `${name}.${RASTER_IMAGES[name].fallback}`)
      expect(statSync(source).isFile(), `manca images/${name}.${RASTER_IMAGES[name].fallback}`).toBe(true)
    }
  })
})

describe('ResponsiveImage', () => {
  const mountImage = (props: { name: RasterImageName, alt: string, sizes?: string, priority?: boolean }) =>
    mount(ResponsiveImage, { props })

  it('offre i formati moderni prima del ripiego, nell\'ordine di preferenza', () => {
    const wrapper = mountImage({ name: 'hero', alt: 'Scrivania', sizes: '100vw' })
    const sources = wrapper.findAll('source')

    expect(sources.map(source => source.attributes('type'))).toEqual(
      MODERN_FORMATS.map(format => `image/${format}`),
    )
    for (const [index, format] of MODERN_FORMATS.entries()) {
      expect(sources[index]!.attributes('srcset')).toBe(srcSet('hero', format))
    }
    expect(wrapper.find('img').attributes('src')).toBe(fallbackUrl('hero'))
  })

  it('dichiara sempre larghezza e altezza native', () => {
    const image = mountImage({ name: 'blog/1', alt: '' }).find('img')
    expect(image.attributes('width')).toBe(String(RASTER_IMAGES['blog/1'].width))
    expect(image.attributes('height')).toBe(String(RASTER_IMAGES['blog/1'].height))
  })

  it('differisce e decodifica in asincrono ciò che non è prioritario', () => {
    const image = mountImage({ name: 'blog/1', alt: '' }).find('img')
    expect(image.attributes('loading')).toBe('lazy')
    expect(image.attributes('decoding')).toBe('async')
    expect(image.attributes('fetchpriority')).toBeUndefined()
  })

  it('non differisce l\'elemento prioritario: differirlo lo ritarderebbe invece di accelerarlo', () => {
    const image = mountImage({ name: 'hero', alt: '', priority: true }).find('img')
    expect(image.attributes('loading')).toBeUndefined()
    expect(image.attributes('fetchpriority')).toBe('high')
  })

  it('omette srcset dove c\'è una sola larghezza: non ci sarebbe niente da scegliere', () => {
    const wrapper = mountImage({ name: 'screenshots/jobs', alt: 'Elenco dei job' })
    expect(wrapper.find('img').attributes('srcset')).toBeUndefined()
    expect(wrapper.findAll('source').every(source => !source.attributes('srcset')!.includes(','))).toBe(true)
  })

  it('passa gli attributi del chiamante all\'immagine e non al contenitore', () => {
    // `<picture>` è dichiarato `display: contents`: una classe che finisse lì
    // non avrebbe nessuna scatola su cui agire.
    const wrapper = mount(ResponsiveImage, {
      props: { name: 'hero', alt: '' },
      attrs: { class: 'copertina' },
    })
    expect(wrapper.find('img').classes()).toContain('copertina')
    expect(wrapper.element.classList.contains('copertina')).toBe(false)
  })
})

describe('markup delle immagini', () => {
  /** Ogni file `.vue` dell'applicazione, componenti, pagine e layout compresi. */
  function vueFiles(dir: string): string[] {
    return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
      const path = join(dir, entry.name)
      if (entry.isDirectory()) return vueFiles(path)
      return entry.name.endsWith('.vue') ? [path] : []
    })
  }

  const files = ['components', 'pages', 'layouts'].flatMap(dir => vueFiles(join(APP_ROOT, dir)))

  /**
   * I tag `<img>` del solo `<template>`, senza i commenti: di `<img>` si parla
   * anche nella documentazione dei componenti, e un tag citato in una frase non
   * è un tag che il browser rende.
   */
  function imageTags(file: string): { file: string, tag: string }[] {
    const source = readFileSync(file, 'utf8')
    const template = /<template>(.*)<\/template>/s.exec(source)?.[1] ?? ''
    return (template.replace(/<!--.*?-->/gs, '').match(/<img\b[^>]*>/gs) ?? [])
      .map(tag => ({ file: relative(APP_ROOT, file), tag: tag.replace(/\s+/g, ' ') }))
  }

  const tags = files.flatMap(imageTags)

  it('trova i tag da controllare: un controllo su zero elementi passerebbe comunque', () => {
    expect(tags.length).toBeGreaterThan(0)
  })

  it('non lascia nessun `<img>` senza larghezza e altezza dichiarate', () => {
    // È da qui che nasce quasi tutto lo spostamento di contenuto: il browser
    // non sa quanto spazio riservare finché l'immagine non arriva, e quando
    // arriva sposta ciò che sta sotto. Il controllo è sul sorgente e non sul
    // reso perché deve valere anche per i componenti che nessun test monta.
    const missing = tags
      .filter(({ tag }) => !/[\s:]width[=\s]/.test(tag) || !/[\s:]height[=\s]/.test(tag))
      .map(({ file, tag }) => `${file}: ${tag.slice(0, 80)}`)

    expect(missing).toEqual([])
  })

  it('non lascia nessun `<img>` senza testo alternativo', () => {
    const missing = tags
      .filter(({ tag }) => !/[\s:]alt[=\s>]/.test(tag))
      .map(({ file, tag }) => `${file}: ${tag.slice(0, 80)}`)

    expect(missing).toEqual([])
  })
})
