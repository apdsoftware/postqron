#!/usr/bin/env node
//
// Legge su stdin il flusso JSON di `govulncheck -format json`, lo riassume e
// decide se la CI deve fallire.
//
// Perché non basta il codice di uscita di govulncheck. In modalità testuale
// govulncheck esce 3 appena trova *qualcosa*, senza distinguere una
// vulnerabilità che il nostro codice chiama da una che sta in un modulo che
// importiamo e non tocchiamo; in modalità JSON esce 0 comunque. Nessuno dei due
// codici è la soglia che ci serve.
//
// E la soglia è il punto. Oggi, a toolchain aggiornata, resta aperta
// GO-2026-5932 su golang.org/x/crypto: è il pacchetto `openpgp`, dichiarato non
// manutenuto, senza versione corretta e senza data in cui ne arriverà una. Non
// lo importiamo. Se bloccasse la CI, la CI sarebbe rossa per sempre per una cosa
// che non possiamo né usare né sistemare — e il primo che deve spingere una
// correzione urgente commenterebbe la riga nel Makefile. Un controllo
// disattivato non protegge nessuno: fallire solo sul raggiungibile è ciò che lo
// tiene acceso.
//
// La distinzione è tutta nel flusso: govulncheck emette un "finding" per ogni
// livello a cui è arrivata l'analisi, e solo quelli risolti fino al simbolo
// hanno `trace[0].function`. Il primo elemento della traccia è il simbolo
// vulnerabile, l'ultimo è il nostro codice che ci arriva.
//
// Uscita: 0 nulla di raggiungibile · 1 almeno una raggiungibile · 2 flusso non
// interpretabile (che non è un verde: se non capiamo l'output non abbiamo
// controllato).

import { readFileSync } from 'node:fs'

// Il protocollo JSON di govulncheck, per la parte che leggiamo. Tipizzarlo qui
// serve a `tsc -p e2e` (checkJs) e a chi dovrà rileggere questo file: è il
// contratto su cui poggia la distinzione fra raggiungibile e no.
/**
 * @typedef {{ module?: string, version?: string, package?: string, function?: string,
 *             position?: { filename: string, line: number } }} Anello
 * @typedef {{ osv: string, fixed_version?: string, trace?: Anello[] }} Finding
 * @typedef {{ scanner_version?: string, db?: string, db_last_modified?: string,
 *             go_version?: string, scan_level?: string }} Config
 * @typedef {{ config?: Config, finding?: Finding,
 *             osv?: { id: string, summary?: string } }} Messaggio
 * @typedef {{ osv: string, livello: 'modulo' | 'pacchetto' | 'simbolo',
 *             correttaIn?: string, dove?: string, versione?: string,
 *             chiamanti: string[] }} Nota
 */

const OK = 0
const RAGGIUNGIBILI = 1
const ILLEGGIBILE = 2

/**
 * @param {number} codice
 * @param {string} messaggio
 * @returns {never}
 */
function esci(codice, messaggio) {
  process.stderr.write(`  ✗ govulncheck: ${messaggio}\n`)
  process.exit(codice)
}

// govulncheck non emette un array ma una sequenza di oggetti JSON concatenati e
// indentati: niente separatore, niente una-riga-un-oggetto. Si scandiscono le
// graffe tenendo conto delle stringhe, che possono contenerne.
/**
 * @param {string} testo
 * @returns {Messaggio[]}
 */
function leggiFlusso(testo) {
  /** @type {Messaggio[]} */
  const messaggi = []
  let profondita = 0
  let inizio = 0
  let inStringa = false
  let escape = false
  for (let i = 0; i < testo.length; i++) {
    const c = testo[i]
    if (inStringa) {
      if (escape) escape = false
      else if (c === '\\') escape = true
      else if (c === '"') inStringa = false
      continue
    }
    if (c === '"') inStringa = true
    else if (c === '{') {
      if (profondita === 0) inizio = i
      profondita++
    } else if (c === '}') {
      profondita--
      if (profondita < 0) throw new Error('graffa chiusa di troppo')
      if (profondita === 0) messaggi.push(JSON.parse(testo.slice(inizio, i + 1)))
    }
  }
  if (profondita !== 0) throw new Error('flusso troncato')
  return messaggi
}

const testo = readFileSync(0, 'utf8')
if (testo.trim() === '') esci(ILLEGGIBILE, 'nessun output da analizzare')

/** @type {Messaggio[]} */
let messaggi = []
try {
  messaggi = leggiFlusso(testo)
} catch (e) {
  esci(ILLEGGIBILE, `output non interpretabile (${e instanceof Error ? e.message : e})`)
}

const config = messaggi.map((m) => m.config).find(Boolean)
if (!config) esci(ILLEGGIBILE, 'il flusso non contiene il messaggio di configurazione')

// Il livello di scansione decide che cosa significa l'assenza di risultati. A
// `-scan module` govulncheck non risolve nessun simbolo: tutto risulterebbe
// irraggiungibile e questo report direbbe «verde» senza aver guardato una riga
// di codice. È esattamente il verde falso da cui nasce la issue #511.
if (config.scan_level !== 'symbol') {
  esci(
    ILLEGGIBILE,
    `livello di scansione «${config.scan_level}»: senza «symbol» nessuna ` +
      'vulnerabilità risulterebbe raggiungibile e il controllo direbbe verde a vuoto',
  )
}

// Un OSV compare più volte, una per livello raggiunto dall'analisi. Conta il
// massimo: se esiste un finding a livello di simbolo, quella vulnerabilità è
// raggiungibile dal nostro codice.
/** @type {Map<string, Nota>} */
const vulnerabilita = new Map()
for (const messaggio of messaggi) {
  const finding = messaggio.finding
  if (!finding) continue
  const traccia = finding.trace ?? []
  const vulnerabile = traccia[0] ?? {}
  const livello = vulnerabile.function ? 'simbolo' : vulnerabile.package ? 'pacchetto' : 'modulo'

  const nota = vulnerabilita.get(finding.osv) ?? {
    osv: finding.osv,
    livello: 'modulo',
    correttaIn: finding.fixed_version,
    dove: vulnerabile.package || vulnerabile.module,
    versione: vulnerabile.version,
    chiamanti: [],
  }
  nota.correttaIn = nota.correttaIn ?? finding.fixed_version
  if (vulnerabile.package) nota.dove = vulnerabile.package

  if (livello === 'simbolo') {
    nota.livello = 'simbolo'
    // Ultimo anello della traccia: il nostro codice. È l'unica informazione
    // azionabile del rapporto — il file da aprire.
    const chiamante = traccia[traccia.length - 1]
    if (chiamante?.position) {
      const pacchetto = (chiamante.package ?? '').split('/').pop()
      const voce = `${chiamante.position.filename}:${chiamante.position.line} — ${pacchetto}.${chiamante.function} → ${vulnerabile.package}.${vulnerabile.function}`
      if (!nota.chiamanti.includes(voce)) nota.chiamanti.push(voce)
    }
  } else if (livello === 'pacchetto' && nota.livello === 'modulo') {
    nota.livello = 'pacchetto'
  }
  vulnerabilita.set(finding.osv, nota)
}

/** @type {Map<string, string>} */
const riassunti = new Map()
for (const messaggio of messaggi) {
  if (messaggio.osv) riassunti.set(messaggio.osv.id, messaggio.osv.summary ?? '')
}

const raggiungibili = [...vulnerabilita.values()].filter((v) => v.livello === 'simbolo')
const altre = [...vulnerabilita.values()].filter((v) => v.livello !== 'simbolo')

/** @param {string} [riga] */
const out = (riga = '') => process.stdout.write(`${riga}\n`)

const dbAggiornato = config.db_last_modified ? config.db_last_modified.slice(0, 10) : 'data ignota'
out(
  `  govulncheck ${config.scanner_version} · ${config.go_version} · ` +
    `db ${config.db} aggiornato al ${dbAggiornato}`,
)
out()

/** @param {Nota[]} vulnerabilitaDaStampare */
function stampa(vulnerabilitaDaStampare) {
  for (const v of vulnerabilitaDaStampare) {
    const correzione = v.correttaIn ? `corretta in ${v.correttaIn}` : 'nessuna versione corretta'
    out(`    · ${v.osv}  ${v.dove}${v.versione ? ` ${v.versione}` : ''} — ${correzione}`)
    if (riassunti.get(v.osv)) out(`      ${riassunti.get(v.osv)}`)
    for (const chiamante of v.chiamanti) out(`      ${chiamante}`)
    out(`      https://pkg.go.dev/vuln/${v.osv}`)
  }
}

if (raggiungibili.length > 0) {
  out(`  ✗ ${raggiungibili.length} vulnerabilità raggiungibili dal nostro codice:`)
  out()
  stampa(raggiungibili)
  out()
}

if (altre.length > 0) {
  out(
    `  ⚠ ${altre.length} in moduli che importiamo ma non chiamiamo — segnalate, non bloccanti:`,
  )
  out()
  stampa(altre)
  out()
}

if (raggiungibili.length > 0) {
  out('  La CI si ferma solo su queste: sono percorsi che il nostro codice esegue.')
  process.exit(RAGGIUNGIBILI)
}

out('  ✓ nessuna vulnerabilità raggiungibile dal nostro codice')
process.exit(OK)
