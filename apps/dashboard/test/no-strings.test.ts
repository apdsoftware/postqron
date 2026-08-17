import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * «Nessuna stringa nei componenti» (SPEC §8-bis), verificato invece che
 * dichiarato.
 *
 * Una frase scritta nel markup non è traducibile: resta in una lingua sola per
 * tutti e cinque i pubblici, e nessuno se ne accorge finché non la legge
 * qualcuno che quella lingua non la parla. È il tipo di difetto che rientra da
 * solo a ogni componente nuovo, quindi non basta toglierlo una volta: serve
 * qualcosa che si accorga del prossimo.
 *
 * Il controllo guarda il markup, non il codice: cerca il testo statico fra i
 * tag e il valore statico degli attributi che l'utente legge o sente. Le
 * espressioni — `{{ ... }}`, `:aria-label="..."` — sono per costruzione
 * contenuto che arriva da `content/`, ed è esattamente ciò che si vuole.
 *
 * È il gemello di `apps/web/test/no-strings.test.ts`: la regola è la stessa in
 * entrambi i frontend, e vale la pena che i due controlli si somiglino.
 */

/*
 * Radice dell'app. Non si ricava da `import.meta.url` per restare identica al
 * controllo del sito pubblico: `vitest run` gira nella cartella del package,
 * che è appunto la radice cercata.
 */
const APP_ROOT = process.cwd()

/** Cartelle che producono markup. `content/` e `utils/` sono dati e logica. */
const MARKUP_DIRS = ['components', 'layouts', 'pages']

/**
 * File di markup fuori dalle cartelle: la radice dell'applicazione.
 *
 * Il controllo del sito pubblico non lo guarda; qui sì, perché `app.vue` è
 * markup a tutti gli effetti e nulla impedirebbe a una frase di finirci dentro.
 */
const ROOT_FILES = ['app.vue']

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
 * Il nome del prodotto non è contenuto traducibile: è un marchio. In cinque
 * lingue si scrive nello stesso modo.
 */
const ALLOWED = ['Postqron']

interface Finding {
  file: string
  line: number
  text: string
}

function vueFiles(dir: string): string[] {
  if (!existsSync(dir)) return []

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
  // Restano cifre, punteggiatura e simboli: `·`, `:`, `.` non sono traducibili.
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

const byDir = MARKUP_DIRS.map(dir => [dir, vueFiles(join(APP_ROOT, dir))] as const)
const files = [
  ...byDir.flatMap(([, found]) => found),
  ...ROOT_FILES.map(name => join(APP_ROOT, name)).filter(path => existsSync(path)),
]

describe('nessuna stringa nei componenti', () => {
  // Un elenco che non pesca niente passerebbe questi test in silenzio, ed è il
  // solo modo in cui il controllo può mentire. Il presidio è per cartella e non
  // sul totale: un conteggio minimo va aggiornato a ogni componente nuovo e
  // nessuno si accorge se una cartella rinominata smette di essere letta.
  it.each(byDir)('%s contribuisce almeno un file da controllare', (_dir, found) => {
    expect(found.length).toBeGreaterThan(0)
  })

  it.each(ROOT_FILES)('%s esiste ed è fra i file controllati', (name) => {
    expect(files).toContain(join(APP_ROOT, name))
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
    const injected = '<template>\n  <p aria-label="Testo statico">Frase nel markup</p>\n</template>'
    const collect = (markup: string) => {
      const template = templateOf(markup)!
      return [...template.markup.matchAll(/>([^<>]*)</g)]
        .map(match => (match[1] ?? '').replace(/\{\{[\s\S]*?\}\}/g, ''))
        .filter(text => !isAllowed(text))
    }

    expect(collect(injected)).toEqual(['Frase nel markup'])
    expect(isAllowed('Testo statico')).toBe(false)
    expect(isAllowed('· . : {{ }}')).toBe(true)
    expect(isAllowed('Postqron')).toBe(true)
  })

  it('riconosce un attributo visibile scritto a mano', () => {
    // L'altra metà del controllo, provata sullo stesso markup finto: un `alt` o
    // un `aria-label` statico non appare sullo schermo e non si vede
    // rileggendo la pagina, ma resta in una lingua sola per chi lo ascolta.
    const attributes = new RegExp(`(^|[\\s(])(${VISIBLE_ATTRIBUTES.join('|')})="([^"]*)"`, 'g')
    const scan = (markup: string) =>
      [...markup.matchAll(attributes)].map(match => match[3]!).filter(value => !isAllowed(value))

    expect(scan('<img alt="Un gatto">')).toEqual(['Un gatto'])
    expect(scan('<select aria-label="Lingua">')).toEqual(['Lingua'])
    // La forma associata è contenuto che arriva da `content/`: non è un reperto.
    expect(scan('<select :aria-label="t.shell.languageLabel">')).toEqual([])
  })
})
