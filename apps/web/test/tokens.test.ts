import { readFileSync, readdirSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * «Il sistema visivo vive nei token» (SPEC §4.0, R35), verificato invece che
 * dichiarato.
 *
 * Un colore scritto a mano dentro un componente non è sbagliato il giorno in
 * cui lo si scrive: lo diventa il giorno in cui la palette cambia e quel
 * componente resta indietro, con una differenza di due punti di luminosità che
 * nessuno nota finché non gliela si mette accanto. È un difetto che rientra da
 * solo a ogni componente nuovo, quindi non basta toglierlo una volta.
 *
 * Il controllo guarda i blocchi `<style>` dei componenti e i fogli globali,
 * `tokens.css` escluso — che è appunto il posto dove i valori vanno scritti.
 */

const APP_ROOT = process.cwd()

const CARTELLE = ['components', 'layouts', 'pages']

/** Fogli globali sotto controllo. `tokens.css` è la sorgente, non un consumatore. */
const FOGLI = ['assets/css/base.css', 'assets/css/layout.css']

/**
 * Proprietà i cui valori appartengono al sistema, non al singolo componente.
 *
 * `width`, `height` e le coordinate di posizionamento restano fuori: la
 * larghezza di un esagono o l'offset di una tendina sono geometria di quel
 * componente, non una misura condivisa che qualcun altro debba riusare.
 */
const PROPRIETA_SPAZIALI = /\b(?:margin|padding|gap|row-gap|column-gap)(?:-[a-z]+)?:\s*([^;]+);/g

const COLORE = /(?:^|[\s:(,])(#[0-9a-fA-F]{3,8})\b/g

/** Corpi e pesi del testo: la scala tipografica è dichiarata una volta sola. */
const TIPOGRAFIA = /\bfont-(?:size|weight):\s*([^;]+);/g

/** Le misure che il sistema dichiara: un literal che le ripete è un token mancato. */
const SPAZI = new Set([5, 10, 15, 20, 25, 30, 35, 40, 50, 60, 70, 80, 140])

interface Ritrovamento {
  file: string
  riga: number
  valore: string
}

function fileConStile(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((voce) => {
    const percorso = join(dir, voce.name)
    if (voce.isDirectory()) return fileConStile(percorso)
    return voce.name.endsWith('.vue') ? [percorso] : []
  })
}

/**
 * Blocchi `<style>` di un componente, tutto il resto sostituito da spazi.
 *
 * Gli spazi non sono cosmetici: tengono ogni carattere alla sua posizione, e
 * quindi il numero di riga che il messaggio d'errore riporta è quello vero.
 */
function stileDi(sorgente: string): string {
  const mascherato = [...sorgente.replace(/[^\n]/g, ' ')]
  for (const blocco of sorgente.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)) {
    const inizio = blocco.index + blocco[0].indexOf('>') + 1
    for (let i = inizio; i < inizio + blocco[1]!.length; i += 1) {
      mascherato[i] = sorgente[i]!
    }
  }
  return mascherato.join('')
}

function rigaDi(sorgente: string, indice: number): number {
  return sorgente.slice(0, indice).split('\n').length
}

function ritrovamenti(file: string, sorgente: string, css: string): Ritrovamento[] {
  const esiti: Ritrovamento[] = []
  const segnala = (indice: number, valore: string) =>
    esiti.push({ file: relative(APP_ROOT, file), riga: rigaDi(sorgente, indice), valore })

  for (const trovato of css.matchAll(COLORE)) {
    segnala(trovato.index + trovato[0].indexOf('#'), trovato[1]!)
  }

  for (const trovato of css.matchAll(TIPOGRAFIA)) {
    if (!/var\(--pq-/.test(trovato[1]!) && /^\s*(?:\d|\.)/.test(trovato[1]!)) {
      segnala(trovato.index, trovato[0].trim())
    }
  }

  for (const trovato of css.matchAll(PROPRIETA_SPAZIALI)) {
    for (const misura of trovato[1]!.matchAll(/\b(\d+)px\b/g)) {
      if (SPAZI.has(Number(misura[1]))) segnala(trovato.index, trovato[0].trim())
    }
  }

  return esiti
}

describe('i valori visivi vivono nei token', () => {
  const file = [
    ...CARTELLE.flatMap(cartella => fileConStile(join(APP_ROOT, cartella))),
    ...FOGLI.map(foglio => join(APP_ROOT, foglio)),
  ]

  it('trova i file da controllare', () => {
    expect(file.length).toBeGreaterThan(10)
  })

  it('nessun componente scrive colori, corpi o spaziature a mano', () => {
    const esiti = file.flatMap((percorso) => {
      const sorgente = readFileSync(percorso, 'utf8')
      const css = percorso.endsWith('.vue') ? stileDi(sorgente) : sorgente
      return ritrovamenti(percorso, sorgente, css)
    })

    const elenco = esiti.map(e => `${e.file}:${e.riga} — ${e.valore}`)
    expect(elenco, 'usare un token di apps/web/assets/css/tokens.css').toEqual([])
  })

  it('si accorge di un valore scritto a mano', () => {
    const finto = '<template><i /></template>\n<style scoped>\n.x { color: #ff0000; }\n</style>'
    expect(ritrovamenti('finto.vue', finto, stileDi(finto))).toEqual([
      { file: expect.any(String), riga: 3, valore: '#ff0000' },
    ])
  })

  it('non guarda fuori dai blocchi di stile', () => {
    const finto = '<script setup lang="ts">\n// vedi #4278e5\n</script>\n<style>.x { top: 0; }</style>'
    expect(ritrovamenti('finto.vue', finto, stileDi(finto))).toEqual([])
  })
})
