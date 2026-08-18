import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

import {
  EXECUTION_STATUSES,
  HEADER_NAME_PATTERN,
  JOB_ALERT_CHANNELS,
  JOB_BACKOFFS,
  JOB_DEFAULTS,
  JOB_ENVIRONMENTS,
  JOB_LIMITS,
  JOB_METHODS,
  JOB_NAME_PATTERN,
  JOB_OVERLAP_POLICIES,
  RESERVED_HEADERS,
} from '~/utils/job-contract'

/**
 * `utils/job-contract.ts` è una copia, e questo test è ciò che le impedisce di
 * diventare una copia **divergente**.
 *
 * Il criterio è quello di `internal/jobs`, dove lo stesso problema esiste fra Go
 * e PostgreSQL e si risolve allo stesso modo: la duplicazione non si toglie —
 * il browser non può interrogare un `CHECK`, e il server non può dare una
 * risposta immediata a chi sta scrivendo — ma si può rendere impossibile
 * lasciarla divergere in silenzio. Qui i sorgenti Go sono letti come testo e
 * ogni valore del client viene confrontato con quello da cui dichiara di venire.
 *
 * Non è un test di comportamento: nessuno di questi confronti prova che la
 * validazione funzioni. Prova che le due parti stiano parlando della stessa
 * regola, che è la cosa che si rompe per prima e che nessuno noterebbe.
 *
 * Se un giorno il backend cambiasse un limite, questo file fallisce nominando
 * la costante — non la schermata che si è rotta tre settimane dopo.
 */

/*
 * `process.cwd()` è `apps/dashboard`: `vitest run` gira nella cartella del
 * package. Da lì il backend è due livelli sopra. È lo stesso ancoraggio di
 * `test/no-strings.test.ts`, e non `import.meta.url`, perché è quello che
 * resiste a uno spostamento del file di test dentro l'app.
 */
const REPO_ROOT = join(process.cwd(), '..', '..')
const JOBS_DIR = join(REPO_ROOT, 'services', 'api', 'internal', 'jobs')

const validateGo = readFileSync(join(JOBS_DIR, 'validate.go'), 'utf8')
const jobsGo = readFileSync(join(JOBS_DIR, 'jobs.go'), 'utf8')

/**
 * Il valore di una costante numerica del blocco `const` di validate.go.
 *
 * Accetta le tre forme che quel file usa: un intero, uno scorrimento di bit
 * (`16 << 10`) e un multiplo di `time.Second`. La terza restituisce secondi,
 * che è l'unità in cui il client ragiona.
 */
function goConst(name: string): number {
  const line = new RegExp(`^\\s*${name}\\s*=\\s*(.+?)\\s*$`, 'm').exec(validateGo)
  expect(line, `costante ${name} non trovata in internal/jobs/validate.go`).not.toBeNull()

  const raw = line![1]!
  const shift = /^(\d+)\s*<<\s*(\d+)$/.exec(raw)
  if (shift) return Number(shift[1]) * 2 ** Number(shift[2])

  const duration = /^(\d+)\s*\*\s*time\.Second$/.exec(raw)
  if (duration) return Number(duration[1])

  const plain = /^(\d+)$/.exec(raw)
  expect(plain, `costante ${name} in forma inattesa: ${raw}`).not.toBeNull()
  return Number(plain![1])
}

/**
 * Il corpo di un `regexp.MustCompile(...)` assegnato alla variabile indicata,
 * così come è scritto fra apici inversi.
 */
function goRegexp(name: string): string {
  // `.*` avido fino all'ultima parentesi della riga: l'espressione stessa
  // contiene parentesi, e fermarsi alla prima taglierebbe a metà proprio il
  // formato del nome.
  const pattern = new RegExp(`^var ${name} = regexp\\.MustCompile\\((.*)\\)\\s*$`, 'm').exec(validateGo)
  expect(pattern, `espressione regolare ${name} non trovata`).not.toBeNull()

  // Go concatena la stringa quando deve includere un apice inverso:
  // `` `^[...` + "`" + `...]+$` ``. Si ricompone estraendone i letterali.
  const literals = [...pattern![1]!.matchAll(/`([^`]*)`|"([^"]*)"/g)]
  return literals.map(match => match[1] ?? match[2] ?? '').join('')
}

/**
 * I valori delle stringhe letterali dichiarate nel blocco `var name = [...]{…}`
 * indicato, nell'ordine in cui compaiono.
 */
function goStringSlice(source: string, name: string): string[] {
  const block = new RegExp(`var ${name} = \\[\\]string\\{([\\s\\S]*?)\\}`).exec(source)
  expect(block, `elenco ${name} non trovato`).not.toBeNull()
  return [...block![1]!.matchAll(/"([^"]*)"/g)].map(match => match[1]!)
}

/**
 * I valori di tutte le costanti di un tipo stringa dichiarato in jobs.go, nel
 * loro ordine di dichiarazione — che per gli ambienti è anche l'ordine del tipo
 * enumerato di PostgreSQL, e conta.
 *
 * Restituisce anche la mappa identificatore → valore, perché `NewJob()` e
 * `DefaultOverlapPolicy` si riferiscono ai nomi, non ai letterali.
 */
function goEnum(type: string): { values: string[], byName: Record<string, string> } {
  const pattern = new RegExp(`^\\s*(\\w+)\\s+${type}\\s*=\\s*"([^"]*)"`, 'gm')
  const values: string[] = []
  const byName: Record<string, string> = {}
  for (const match of jobsGo.matchAll(pattern)) {
    values.push(match[2]!)
    byName[match[1]!] = match[2]!
  }
  expect(values.length, `nessuna costante di tipo ${type} in internal/jobs/jobs.go`).toBeGreaterThan(0)
  return { values, byName }
}

/** Il corpo del letterale restituito da `NewJob()`. */
const newJobBody = (() => {
  const block = /func NewJob\(\) Job \{\s*return Job\{([\s\S]*?)\n\t\}/.exec(jobsGo)
  expect(block, 'corpo di NewJob() non trovato in internal/jobs/jobs.go').not.toBeNull()
  return block![1]!
})()

/** Il valore assegnato a un campo dentro `NewJob()`, così come è scritto. */
function newJobField(field: string): string {
  const line = new RegExp(`^\\s*${field}:\\s*(.+?),\\s*$`, 'm').exec(newJobBody)
  expect(line, `campo ${field} non assegnato in NewJob()`).not.toBeNull()
  return line![1]!
}

// ------------------------------------------------------------------- prove

describe('limiti di forma', () => {
  it.each([
    ['maxNameLength', 'MaxNameLength'],
    ['maxDescriptionLength', 'MaxDescriptionLength'],
    ['maxUrlLength', 'MaxURLLength'],
    ['maxHeaders', 'MaxHeaders'],
    ['maxHeaderNameLength', 'MaxHeaderNameLength'],
    ['maxHeaderValueLength', 'MaxHeaderValueLength'],
    ['maxBodyLength', 'MaxBodyLength'],
    ['minTimeoutSeconds', 'MinTimeout'],
    ['maxTimeoutSeconds', 'MaxTimeout'],
    ['maxRetries', 'MaxRetriesAllowed'],
  ] as const)('%s è jobs.%s', (chiave, costante) => {
    expect(JOB_LIMITS[chiave]).toBe(goConst(costante))
  })

  it('non dichiara limiti che il backend non ha', () => {
    // Il senso del controllo è **la direzione**: un limite in più qui è un
    // modulo che si rifiuta di inviare per una regola che il server non ha, ed è
    // un difetto tanto quanto il contrario. L'elenco delle chiavi è chiuso e
    // ogni voce è confrontata sopra; aggiungerne una senza il confronto rompe
    // questo test.
    expect(Object.keys(JOB_LIMITS).sort()).toEqual([
      'maxBodyLength', 'maxDescriptionLength', 'maxHeaderNameLength',
      'maxHeaderValueLength', 'maxHeaders', 'maxNameLength', 'maxRetries',
      'maxTimeoutSeconds', 'maxUrlLength', 'minTimeoutSeconds',
    ])
  })
})

describe('espressioni regolari', () => {
  it('il formato del nome è quello di jobs.nameFormat', () => {
    expect(JOB_NAME_PATTERN.source).toBe(goRegexp('nameFormat'))
  })

  it('il formato del nome di un header è quello di jobs.headerNameFormat', () => {
    expect(HEADER_NAME_PATTERN.source).toBe(goRegexp('headerNameFormat'))
  })

  it('riconosce gli stessi nomi che il database accetterebbe', () => {
    // Qualche caso concreto accanto al confronto testuale: un'espressione
    // identica scritta in due dialetti diversi potrebbe comportarsi in modo
    // diverso, e questi sono i casi che `jobs_name_format_check` distingue.
    for (const valido of ['a', 'daily-digest', 'job.1', 'A_B', 'x0']) {
      expect(JOB_NAME_PATTERN.test(valido), valido).toBe(true)
    }
    for (const invalido of ['', '-a', 'a-', '.a', 'a b', 'à', 'a/b']) {
      expect(JOB_NAME_PATTERN.test(invalido), invalido).toBe(false)
    }
  })
})

describe('domini chiusi', () => {
  it('gli header riservati sono quelli di jobs.reservedHeaders', () => {
    expect([...RESERVED_HEADERS]).toEqual(goStringSlice(validateGo, 'reservedHeaders'))
  })

  it.each([
    ['JOB_METHODS', JOB_METHODS, 'Method'],
    ['JOB_BACKOFFS', JOB_BACKOFFS, 'Backoff'],
    ['JOB_OVERLAP_POLICIES', JOB_OVERLAP_POLICIES, 'OverlapPolicy'],
    ['JOB_ALERT_CHANNELS', JOB_ALERT_CHANNELS, 'AlertChannel'],
    ['JOB_ENVIRONMENTS', JOB_ENVIRONMENTS, 'Environment'],
    ['EXECUTION_STATUSES', EXECUTION_STATUSES, 'ExecutionStatus'],
  ] as const)('%s elenca le costanti di jobs.%s, nello stesso ordine', (_nome, valori, tipo) => {
    // L'ordine conta almeno per gli ambienti: PostgreSQL ordina un enum per
    // dichiarazione, non alfabeticamente, e il cursore di paginazione delle
    // esecuzioni segue quello stesso criterio (`jobs.EnvironmentRank`).
    expect([...valori]).toEqual(goEnum(tipo).values)
  })
})

describe('valori predefiniti di un job nuovo', () => {
  it('il fuso è quello di NewJob()', () => {
    expect(`"${JOB_DEFAULTS.timezone}"`).toBe(newJobField('Timezone'))
  })

  it('il timeout è quello di NewJob()', () => {
    expect(newJobField('Timeout')).toBe(`${JOB_DEFAULTS.timeoutSeconds} * time.Second`)
  })

  it('i tentativi sono quelli di NewJob()', () => {
    expect(newJobField('MaxRetries')).toBe(String(JOB_DEFAULTS.maxRetries))
  })

  it('il metodo, la politica di attesa e lo stato sono quelli di NewJob()', () => {
    expect(goEnum('Method').byName[newJobField('Method')]).toBe(JOB_DEFAULTS.method)
    expect(goEnum('Backoff').byName[newJobField('RetryBackoff')]).toBe(JOB_DEFAULTS.retryBackoff)
    expect(newJobField('Enabled')).toBe(String(JOB_DEFAULTS.enabled))
  })

  it('gli ambienti e i canali di avviso sono quelli di NewJob()', () => {
    const ambienti = goEnum('Environment').byName
    const canali = goEnum('AlertChannel').byName
    /*
     * Solo ciò che sta dentro le graffe: `[]Environment{EnvironmentProduction}`
     * nomina il tipo prima del valore, e prenderli entrambi confronterebbe una
     * lista di due elementi con una di uno.
     */
    const nomi = (raw: string): string[] => {
      const dentro = /\{([^}]*)\}/.exec(raw)
      expect(dentro, `letterale di lista atteso, trovato: ${raw}`).not.toBeNull()
      return dentro![1]!.split(',').map(nome => nome.trim()).filter(nome => nome !== '')
    }

    expect(nomi(newJobField('Environments')).map(nome => ambienti[nome]))
      .toEqual([...JOB_DEFAULTS.environments])
    expect(nomi(newJobField('AlertOnFailure')).map(nome => canali[nome]))
      .toEqual([...JOB_DEFAULTS.alertOnFailure])
  })

  it('la politica di sovrapposizione è jobs.DefaultOverlapPolicy', () => {
    // `NewJob()` scrive `DefaultOverlapPolicy`, che a sua volta è una costante:
    // si segue il rimando invece di fidarsi del nome, perché è proprio il valore
    // dietro il nome che R41 pretende sia dichiarato.
    expect(newJobField('OverlapPolicy')).toBe('DefaultOverlapPolicy')

    const rimando = /const DefaultOverlapPolicy = (\w+)/.exec(jobsGo)
    expect(rimando, 'DefaultOverlapPolicy non trovata').not.toBeNull()
    expect(goEnum('OverlapPolicy').byName[rimando![1]!]).toBe(JOB_DEFAULTS.overlapPolicy)
  })
})

describe('il presidio sa fallire', () => {
  it('si accorge di un numero che non corrisponde più', () => {
    // Un test che confronta sorgenti veri passa anche se l'estrazione non pesca
    // niente e confronta `undefined` con `undefined`. Qui si verifica che le
    // funzioni di lettura leggano davvero.
    expect(goConst('MaxNameLength')).toBe(100)
    expect(goConst('MaxBodyLength')).toBe(16384)
    expect(goConst('MaxTimeout')).toBe(300)
    expect(goRegexp('nameFormat')).toContain('a-zA-Z0-9')
    expect(goEnum('Method').values).toContain('GET')
    expect(goStringSlice(validateGo, 'reservedHeaders')).toContain('host')
    expect(newJobField('Timezone')).toBe('"UTC"')
  })
})
