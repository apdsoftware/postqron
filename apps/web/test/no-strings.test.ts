import { readFileSync, readdirSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * «Nessuna stringa nei componenti» (SPEC §8-bis), verificato invece che
 * dichiarato.
 *
 * Una frase scritta nel markup non è traducibile: resta in una lingua sola in
 * tutte e cinque le versioni della pagina, e nessuno se ne accorge finché non
 * la legge qualcuno che quella lingua non la parla. È il tipo di difetto che
 * rientra da solo a ogni componente nuovo, quindi non basta toglierlo una
 * volta: serve qualcosa che si accorga del prossimo.
 *
 * Il controllo guarda il markup, non il codice: cerca il testo statico fra i
 * tag e il valore statico degli attributi che l'utente legge o sente. Le
 * espressioni — `{{ ... }}`, `:alt="..."` — sono per costruzione contenuto che
 * arriva da `content/`, ed è esattamente ciò che si vuole.
 */

/*
 * Radice dell'app. Non si ricava da `import.meta.url`: nell'ambiente Nuxt di
 * Vitest i moduli non hanno un URL `file:`. `vitest run` gira nella cartella
 * del package, che è appunto la radice cercata.
 */
const APP_ROOT = process.cwd()

/** Cartelle che producono markup. `content/` e `utils/` sono dati e logica. */
const MARKUP_DIRS = ['components', 'layouts', 'pages']

/** Attributi il cui valore finisce sotto gli occhi o nella sintesi vocale. */
const VISIBLE_ATTRIBUTES = [
  'alt',
  'title',
  'label',
  'placeholder',
  'aria-label',
  'aria-description',
  'aria-placeholder',
  'aria-roledescription',
  'aria-valuetext',
]

/**
 * Testo ammesso nel markup.
 *
 * Il nome del prodotto non è contenuto traducibile: è un marchio, e nel logo è
 * un tracciato tipografico. In cinque lingue si scrive nello stesso modo.
 */
const ALLOWED = ['PostQron']

interface Finding {
  file: string
  line: number
  text: string
}

function vueFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return vueFiles(path)
    return entry.name.endsWith('.vue') ? [path] : []
  })
}

/** Blocco `<template>` di primo livello, commenti esclusi. */
function templateOf(source: string): { markup: string, offset: number } | null {
  const start = source.indexOf('<template>')
  const end = source.lastIndexOf('</template>')
  if (start === -1 || end === -1) return null

  const markup = source.slice(start, end)
  // I commenti restano al loro posto come spazi: così le posizioni non slittano.
  return {
    markup: markup.replace(/<!--[\s\S]*?-->/g, match => ' '.repeat(match.length)),
    offset: start,
  }
}

function lineAt(source: string, index: number): number {
  return source.slice(0, index).split('\n').length
}

function isAllowed(text: string): boolean {
  const stripped = ALLOWED.reduce((acc, term) => acc.split(term).join(''), text)
  // Restano cifre, punteggiatura e simboli: `©`, `:`, `.` non sono traducibili.
  return !/\p{L}{2}/u.test(stripped)
}

function findingsIn(file: string): Finding[] {
  const source = readFileSync(file, 'utf8')
  const template = templateOf(source)
  if (!template) return []

  const name = relative(APP_ROOT, file)
  const findings: Finding[] = []

  // Testo fra i tag, tolte le interpolazioni.
  for (const match of template.markup.matchAll(/>([^<>]*)</g)) {
    const text = (match[1] ?? '').replace(/\{\{[\s\S]*?\}\}/g, '')
    if (isAllowed(text)) continue

    findings.push({
      file: name,
      line: lineAt(source, template.offset + match.index + 1),
      text: text.trim(),
    })
  }

  // Attributi statici: quelli associati (`:alt`, `v-bind:alt`) non ricadono qui.
  const attributes = new RegExp(`(^|[\\s(])(${VISIBLE_ATTRIBUTES.join('|')})="([^"]*)"`, 'g')
  for (const match of template.markup.matchAll(attributes)) {
    const value = match[3] ?? ''
    if (isAllowed(value)) continue

    findings.push({
      file: name,
      line: lineAt(source, template.offset + match.index),
      text: `${match[2]}="${value}"`,
    })
  }

  return findings
}

const files = MARKUP_DIRS.flatMap(dir => vueFiles(join(APP_ROOT, dir)))

describe('nessuna stringa nei componenti', () => {
  it('trova comunque i file da controllare', () => {
    // Un glob che non pesca niente passerebbe questo test in silenzio, ed è il
    // solo modo in cui il controllo può mentire.
    expect(files.length).toBeGreaterThan(15)
  })

  it.each(files.map(file => [relative(APP_ROOT, file), file]))(
    '%s non contiene testo scritto nel markup',
    (_name, file) => {
      expect(findingsIn(file)).toEqual([])
    },
  )

  it('riconosce una frase reintrodotta nel markup', () => {
    // Il controllo deve poter fallire: verificato su markup finto, perché su
    // quello vero — giustamente — non fallisce mai.
    const injected = '<template>\n  <p alt="Testo statico">Frase nel markup</p>\n</template>'
    const collect = (markup: string) => {
      const template = templateOf(markup)!
      return [...template.markup.matchAll(/>([^<>]*)</g)]
        .map(match => (match[1] ?? '').replace(/\{\{[\s\S]*?\}\}/g, ''))
        .filter(text => !isAllowed(text))
    }

    expect(collect(injected)).toEqual(['Frase nel markup'])
    expect(isAllowed('Testo statico')).toBe(false)
    expect(isAllowed('© . : {{ }}')).toBe(true)
    expect(isAllowed('PostQron')).toBe(true)
  })
})
