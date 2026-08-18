/**
 * Quando parte il job: le prossime occorrenze di una schedulazione, calcolate
 * nel browser.
 *
 * ## Perché esiste, visto che il calcolo lo fa il motore
 *
 * Un'espressione cron è difficile da leggere e facilissima da sbagliare:
 * `0 0 * * 0` e `0 0 0 * *` si somigliano e vogliono dire cose diversissime —
 * ogni domenica a mezzanotte la prima, mai la seconda, perché lo zero non è un
 * giorno del mese valido. Mostrare **gli orari veri**, mentre si scrive, è ciò
 * che trasforma un campo di testo in uno strumento.
 *
 * Il valore autorevole c'è già e si chiama `next_run_at` — lo calcola lo
 * scheduler, non noi — ma non serve a questo: su un job appena creato è `null`
 * per costruzione (`jobs.Service.Create`, migrazione 0010: il job nasce senza e
 * il motore lo raccoglie alla passata successiva), e su un job che si sta
 * modificando descrive la schedulazione *di prima*. L'unico momento in cui
 * all'utente serve sapere quando partirà è proprio quello in cui il backend non
 * può dirglielo: mentre lo sta scrivendo.
 *
 * ## Le due cose che il backend sa e il browser no
 *
 * **Il fuso è del job, non del browser** (R1). Un'anteprima renderizzata
 * nell'ora locale di chi guarda sarebbe sbagliata per chiunque non viva nel fuso
 * che ha scritto nel modulo, e sbagliata in un modo che sembra giusto. Ogni
 * istante qui dentro è calcolato nel fuso del job e va mostrato dichiarandolo.
 *
 * **L'intervallo è ancorato all'epoch** (SPEC §9). `every: 1h` scocca all'ora
 * piena UTC, non un'ora dopo il salvataggio: le occorrenze cadono a
 * `epoch + k·intervallo` e non dipendono da quando il job è stato creato né da
 * quando il processo è partito. Un'anteprima che mostrasse «fra un'ora» sarebbe
 * l'errore più convincente possibile.
 *
 * ## Perché è un porto e non una libreria
 *
 * Le regole non sono quelle di nessuna libreria di cron: sono quelle scelte in
 * `services/api/internal/schedule`, e su due punti — l'ora che non esiste e
 * l'ora che accade due volte — sono deliberatamente diverse da quelle che una
 * libreria applicherebbe da sé. Un'anteprima che dicesse un istante e un motore
 * che ne eseguisse un altro sarebbe peggio di nessuna anteprima: sarebbe una
 * promessa. `test/schedule.test.ts` ripercorre gli stessi casi del pacchetto Go,
 * con gli stessi istanti attesi.
 *
 * Il porto è anche la scelta più leggera: `cron-parser` più `luxon` sono decine
 * di kilobyte, questo file ne è qualcuno, e i fusi orari li conosce già il
 * browser attraverso `Intl` (R53-bis, il tetto sul JavaScript d'avvio).
 *
 * Nessuna dipendenza da Vue né da Nuxt: logica pura, verificabile senza montare
 * niente.
 */

/** Le due modalità di schedulazione, come le dichiara SPEC §9. */
export interface ScheduleSpec {
  /** Espressione cron a cinque campi. Vuota se il job usa l'intervallo. */
  expression?: string
  /** Intervallo in secondi. Zero se il job usa il cron. */
  everySeconds?: number
  /** Nome IANA del fuso. Vuoto vale `UTC`, come la colonna `jobs.timezone`. */
  timezone?: string
}

/**
 * Perché una schedulazione non si è potuta leggere.
 *
 * Sono **chiavi**, non frasi: il messaggio che l'utente legge sta in
 * `content/`, in cinque lingue (SPEC §8-bis). I codici ricalcano i casi che
 * `internal/schedule` distingue, perché è su quelli che cambia cosa c'è da
 * fare.
 */
export type ScheduleErrorCode
  /** Né `schedule` né `every`: `ErrNoMode`. */
  = | 'noMode'
  /** Tutti e due: `ErrBothModes`. */
    | 'bothModes'
  /** Il fuso non è un nome IANA che il browser conosca. */
    | 'unknownTimezone'
  /** `Local` non è un fuso: dipende dalla macchina. */
    | 'localTimezone'
  /** L'espressione non ha cinque campi. */
    | 'fieldCount'
  /** Un'abbreviazione `@daily` e simili, che il database non accetterebbe. */
    | 'macro'
  /** Un campo dell'espressione non si legge. Vedi [ScheduleError.field]. */
    | 'invalidField'
  /** L'intervallo non è un numero intero positivo di secondi. */
    | 'invalidInterval'

export interface ScheduleError {
  code: ScheduleErrorCode
  /**
   * Nome del campo dell'espressione a cui l'errore si riferisce, quando ce n'è
   * uno: `minute`, `hour`, `dayOfMonth`, `month`, `dayOfWeek`. Serve a dire
   * *dove*, che è la differenza fra un errore utile e «espressione non valida».
   */
  field?: CronFieldName
  /** Il testo che non si è potuto leggere, per mostrarlo accanto al messaggio. */
  value?: string
}

export type CronFieldName = 'minute' | 'hour' | 'dayOfMonth' | 'month' | 'dayOfWeek'

/**
 * Una schedulazione compilata: l'unica cosa che sa fare è dire quando tocca al
 * job la prossima volta.
 */
export interface CompiledSchedule {
  kind: 'cron' | 'interval'
  /** Fuso in cui gli istanti vanno mostrati. Sempre valorizzato. */
  timezone: string
  /**
   * La prima occorrenza **strettamente successiva** ad `after`. `null` quando
   * non esiste: succede alle espressioni su date impossibili (`0 0 30 2 *`),
   * mai agli intervalli.
   *
   * La stretta disuguaglianza è la stessa garanzia di `Schedule.Next`:
   * ripassare l'ultimo istante restituito produce sempre un avanzamento,
   * quindi nessuna occorrenza compare due volte nell'anteprima.
   */
  next: (after: Date) => Occurrence | null
}

// ------------------------------------------------------------------- fusi

/**
 * Formattatori per fuso, tenuti da parte.
 *
 * Costruire un `Intl.DateTimeFormat` non è gratis e qui se ne chiede l'offset
 * decine di volte per ogni occorrenza — la ricerca binaria del buco di ora
 * legale da sola ne fa una trentina. Il numero di fusi distinti in una pagina è
 * uno.
 */
const formatters = new Map<string, Intl.DateTimeFormat>()

function zoneFormatter(timezone: string): Intl.DateTimeFormat {
  const cached = formatters.get(timezone)
  if (cached) return cached

  const formatter = new Intl.DateTimeFormat('en-US', {
    timeZone: timezone,
    // `hourCycle: 'h23'` e non `hour12: false`: la seconda, su alcuni motori,
    // rende la mezzanotte come «24» del giorno prima, e l'aritmetica che segue
    // finirebbe un giorno indietro.
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  formatters.set(timezone, formatter)
  return formatter
}

/**
 * Verifica che il fuso esista, e lo normalizza: vuoto vale `UTC`.
 *
 * `Local` è rifiutato esplicitamente perché `internal/schedule` lo rifiuta —
 * dipende dall'ambiente del processo — e il client non deve accettare ciò che il
 * server respinge più di quanto debba rifiutare ciò che il server accetta.
 *
 * Il riconoscimento passa da `Intl`, che accetta anche i nomi storici
 * (`US/Pacific`) oltre a quelli canonici: è la stessa larghezza di
 * `time.LoadLocation`, e restringerla qui vorrebbe dire rifiutare fusi che il
 * backend accetta — cioè il difetto peggiore dei due.
 */
export function resolveTimezone(timezone: string | undefined): string | ScheduleError {
  const name = (timezone ?? '').trim()
  if (name === '') return 'UTC'
  if (name === 'Local') return { code: 'localTimezone', value: name }

  try {
    zoneFormatter(name)
  }
  catch {
    return { code: 'unknownTimezone', value: name }
  }
  return name
}

/**
 * Orario da parete di un istante nel fuso indicato, in millisecondi dall'epoch
 * **come se fosse UTC**.
 *
 * È lo stesso espediente di `wallClock` in `internal/schedule/zone.go`:
 * l'aritmetica di calendario — fine mese, anni bisestili — è già scritta e
 * corretta in UTC, che è l'unico fuso in cui nessun salto d'ora la falsa.
 */
function wallOf(instant: number, timezone: string): number {
  const parts = zoneFormatter(timezone).formatToParts(new Date(instant))
  const field = (type: Intl.DateTimeFormatPartTypes): number => {
    const found = parts.find(part => part.type === type)
    return found === undefined ? 0 : Number(found.value)
  }
  return Date.UTC(
    field('year'), field('month') - 1, field('day'),
    field('hour'), field('minute'), field('second'),
  )
}

/** Scostamento dal meridiano di Greenwich, in secondi, all'istante indicato. */
function offsetAt(instant: number, timezone: string): number {
  return Math.round((wallOf(instant, timezone) - instant) / 1000)
}

/**
 * Come si è tradotto un orario da parete in un istante. Sono i tre casi di
 * `resolution` in zone.go, e vale la pena distinguerli anche qui: l'anteprima
 * li **dichiara**, perché un'occorrenza che si sposta di mezz'ora senza
 * spiegazione sembra un difetto.
 */
export type Resolution = 'exact' | 'gap' | 'ambiguous'

export interface Occurrence {
  /** L'istante in cui il job parte. */
  at: Date
  /** Come ci si è arrivati: vedi [Resolution]. */
  resolution: Resolution
}

/**
 * Gli scostamenti che possono valere per l'istante cercato.
 *
 * L'istante che corrisponde a un orario da parete dista da esso al massimo
 * quanto lo scostamento stesso, cioè meno di quindici ore in entrambe le
 * direzioni. Sondare il fuso in una manciata di punti dentro quella finestra
 * basta a pescare quelli in gioco: fra due sonde consecutive un fuso reale non
 * cambia più di una volta, e uno in più o in meno non fa danno perché ogni
 * candidato viene comunque verificato.
 */
const PROBES_MS = [
  -26 * 3600_000, -15 * 3600_000, -2 * 3600_000, -3600_000,
  0, 3600_000, 2 * 3600_000, 13 * 3600_000, 26 * 3600_000,
]

function candidateOffsets(wall: number, timezone: string): number[] {
  const offsets: number[] = []
  for (const probe of PROBES_MS) {
    const offset = offsetAt(wall + probe, timezone)
    if (!offsets.includes(offset)) offsets.push(offset)
  }
  return offsets
}

/**
 * Traduce un orario da parete nell'istante in cui il job parte, applicando le
 * due regole scelte in `internal/schedule` — che **non** sono quelle che una
 * libreria applicherebbe da sé, ed è tutto il motivo per cui questo codice
 * esiste invece di una dipendenza:
 *
 * - **l'ora che non esiste** (l'ultima domenica di marzo, le 02:30 a Roma) non
 *   si salta: l'occorrenza si sposta al primo istante che esiste. Un backup
 *   giornaliero che una volta l'anno non parte è un guasto silenzioso, e chi lo
 *   subisce non lo attribuisce mai all'ora legale;
 * - **l'ora che accade due volte** (l'ultima domenica di ottobre) esegue solo
 *   alla prima. Due esecuzioni sarebbero due fatture, due digest, due invii, e
 *   la chiave naturale di `job_executions` non le intercetterebbe perché hanno
 *   `scheduled_for` diversi.
 */
function resolveWall(wall: number, timezone: string): { instant: number, resolution: Resolution } {
  const offsets = candidateOffsets(wall, timezone)

  /*
   * Un istante `x` corrisponde all'orario da parete `wall` se e solo se
   * x + offset(x) == wall. Per ogni scostamento plausibile si calcola il
   * candidato e si verifica che a quell'istante lo scostamento sia davvero
   * quello: è il controllo che distingue i tre casi senza assumerne nessuno.
   */
  const valid: number[] = []
  for (const offset of offsets) {
    const candidate = wall - offset * 1000
    if (offsetAt(candidate, timezone) === offset) valid.push(candidate)
  }

  if (valid.length === 1) return { instant: valid[0]!, resolution: 'exact' }
  if (valid.length > 1) return { instant: Math.min(...valid), resolution: 'ambiguous' }
  return { instant: gapEnd(wall, offsets, timezone), resolution: 'gap' }
}

/**
 * L'istante in cui finisce il buco che contiene l'orario da parete, che per la
 * regola scelta è l'istante in cui il job parte.
 *
 * Il buco è delimitato: con il massimo e il minimo fra gli scostamenti
 * plausibili, `wall - max` cade sicuramente prima del salto e `wall - min`
 * sicuramente dopo. In mezzo la funzione «istante → orario da parete» cresce di
 * un secondo al secondo e fa un unico scalino verso l'alto, quindi una ricerca
 * binaria trova il primo istante il cui orario da parete raggiunge quello
 * cercato: è il salto.
 */
function gapEnd(wall: number, offsets: number[], timezone: string): number {
  // Orario da parete di un istante, nella stessa unità del bersaglio: secondi
  // dall'epoch a cui è sommato lo scostamento del fuso.
  const wallSeconds = (seconds: number): number => seconds + offsetAt(seconds * 1000, timezone)
  const target = wall / 1000

  // Con un solo scostamento in gioco non c'è nessun salto e ogni orario da
  // parete ha il suo istante: irraggiungibile, ma un fuso più strano di quanto
  // questo codice sappia trattare deve dare un istante reale, non un valore
  // inventato.
  if (offsets.length < 2) return wall - offsets[0]! * 1000

  let lo = target - Math.max(...offsets)
  let hi = target - Math.min(...offsets)
  if (wallSeconds(lo) >= target || wallSeconds(hi) <= target) {
    // Gli estremi non racchiudono il salto, quindi l'ipotesi su cui la ricerca
    // binaria si regge non vale: stessa ragione di sopra.
    return wall - Math.max(...offsets) * 1000
  }

  while (hi - lo > 1) {
    const mid = lo + Math.floor((hi - lo) / 2)
    if (wallSeconds(mid) < target) lo = mid
    else hi = mid
  }
  return hi * 1000
}

// ------------------------------------------------------------------- cron

interface CronFieldDef {
  name: CronFieldName
  min: number
  max: number
  names?: Record<string, number>
}

const MONTH_NAMES: Record<string, number> = {
  JAN: 1, FEB: 2, MAR: 3, APR: 4, MAY: 5, JUN: 6,
  JUL: 7, AUG: 8, SEP: 9, OCT: 10, NOV: 11, DEC: 12,
}

const DAY_NAMES: Record<string, number> = {
  SUN: 0, MON: 1, TUE: 2, WED: 3, THU: 4, FRI: 5, SAT: 6,
}

/**
 * I cinque campi, nell'ordine in cui compaiono. Il giorno della settimana
 * arriva a 7 perché il crontab classico accetta sia `0` sia `7` per la
 * domenica; la fusione dei due avviene subito dopo il parsing.
 */
const CRON_FIELDS: readonly CronFieldDef[] = [
  { name: 'minute', min: 0, max: 59 },
  { name: 'hour', min: 0, max: 23 },
  { name: 'dayOfMonth', min: 1, max: 31 },
  { name: 'month', min: 1, max: 12, names: MONTH_NAMES },
  { name: 'dayOfWeek', min: 0, max: 7, names: DAY_NAMES },
]

/**
 * Le abbreviazioni del crontab. Non le accettiamo — il vincolo
 * `jobs_schedule_shape_check` pretende cinque campi, e un'espressione che il
 * client accetta e il database rifiuta è peggio di un rifiuto netto — ma le
 * riconosciamo per poter dire cosa scrivere al loro posto.
 */
const MACROS: Record<string, string> = {
  '@yearly': '0 0 1 1 *',
  '@annually': '0 0 1 1 *',
  '@monthly': '0 0 1 * *',
  '@weekly': '0 0 * * 0',
  '@daily': '0 0 * * *',
  '@midnight': '0 0 * * *',
  '@hourly': '0 * * * *',
}

/**
 * Anni entro cui cercare prima di dichiarare che un'occorrenza non esiste. Otto
 * coprono il caso peggiore reale, il 29 febbraio: fra il 2096 e il 2104 ce ne
 * sono otto, perché il 2100 non è bisestile.
 */
const SEARCH_YEARS = 8

interface CronExpression {
  minute: Set<number>
  hour: Set<number>
  dayOfMonth: Set<number>
  month: Set<number>
  dayOfWeek: Set<number>
  domRestricted: boolean
  dowRestricted: boolean
}

function parseValue(text: string, def: CronFieldDef): number | string {
  if (text === '') return 'empty'
  if (def.names) {
    const named = def.names[text.toUpperCase()]
    if (named !== undefined) return named
  }
  if (!/^\d+$/.test(text)) return 'notANumber'
  const value = Number(text)
  if (value < def.min || value > def.max) return 'outOfRange'
  return value
}

function parseField(field: string, def: CronFieldDef): Set<number> | ScheduleError {
  const fail = (): ScheduleError => ({ code: 'invalidField', field: def.name, value: field })
  const values = new Set<number>()

  for (const term of field.split(',')) {
    if (term === '') return fail()

    let body = term
    let step = 1
    const slash = term.indexOf('/')
    if (slash >= 0) {
      body = term.slice(0, slash)
      const raw = term.slice(slash + 1)
      if (!/^\d+$/.test(raw) || Number(raw) < 1) return fail()
      step = Number(raw)
      // Il crontab classico ammette il passo solo dopo `*` o dopo un
      // intervallo: `5/15` non vuol dire niente di definito, e accettarlo con
      // un'interpretazione a caso è peggio che rifiutarlo.
      if (body !== '*' && !body.includes('-')) return fail()
    }

    let lo: number
    let hi: number
    if (body === '*') {
      lo = def.min
      hi = def.max
    }
    else {
      const dash = body.indexOf('-')
      if (dash >= 0) {
        const start = parseValue(body.slice(0, dash), def)
        const end = parseValue(body.slice(dash + 1), def)
        if (typeof start === 'string' || typeof end === 'string') return fail()
        // Il crontab classico non ammette intervalli che scavalcano il fondo
        // scala: `FRI-MON` sembra sensato e non lo è.
        if (start > end) return fail()
        lo = start
        hi = end
      }
      else {
        const single = parseValue(body, def)
        if (typeof single === 'string') return fail()
        lo = single
        hi = single
      }
    }

    for (let v = lo; v <= hi; v += step) values.add(v)
  }

  return values
}

function parseCron(expression: string): CronExpression | ScheduleError {
  const trimmed = expression.trim()
  if (trimmed === '') return { code: 'noMode' }
  if (MACROS[trimmed.toLowerCase()] !== undefined || trimmed.startsWith('@')) {
    return { code: 'macro', value: trimmed }
  }

  const fields = trimmed.split(/\s+/)
  if (fields.length !== 5) return { code: 'fieldCount', value: trimmed }

  const sets: Set<number>[] = []
  for (let i = 0; i < CRON_FIELDS.length; i += 1) {
    const parsed = parseField(fields[i]!, CRON_FIELDS[i]!)
    if (parsed instanceof Set) sets.push(parsed)
    else return parsed
  }

  // `7` e `0` sono lo stesso giorno: si fondono, così il confronto con il
  // giorno della settimana non deve più saperlo.
  const dayOfWeek = sets[4]!
  if (dayOfWeek.has(7)) {
    dayOfWeek.delete(7)
    dayOfWeek.add(0)
  }

  return {
    minute: sets[0]!,
    hour: sets[1]!,
    dayOfMonth: sets[2]!,
    month: sets[3]!,
    dayOfWeek,
    domRestricted: fields[2] !== '*',
    dowRestricted: fields[4] !== '*',
  }
}

/**
 * La regola dei due campi dei giorni, che è quella del cron di Vixie: quando
 * **entrambi** sono ristretti vale l'unione, non l'intersezione. Senza,
 * `0 0 13 * FRI` — «il 13 del mese oppure ogni venerdì» — significherebbe «solo
 * i venerdì 13», che è un'altra cosa e capita due volte l'anno.
 */
function matchesDay(cron: CronExpression, wall: Date): boolean {
  const dom = cron.dayOfMonth.has(wall.getUTCDate())
  const dow = cron.dayOfWeek.has(wall.getUTCDay())
  if (cron.domRestricted && cron.dowRestricted) return dom || dow
  return dom && dow
}

/**
 * La prima occorrenza dell'espressione strettamente successiva ad `after`.
 *
 * La ricerca avanza sull'**orologio da parete**, non sugli istanti: è l'orologio
 * che l'espressione descrive. Ogni orario candidato viene poi tradotto in
 * istante applicando le regole sull'ora legale, e scartato se non è strettamente
 * successivo. Quel confronto finale fa due lavori in uno: garantisce
 * l'avanzamento, e fa sì che le occorrenze collassate sullo stesso istante
 * dentro un buco producano una sola esecuzione — senza codice dedicato.
 */
function nextCron(
  cron: CronExpression,
  timezone: string,
  after: Date,
): Occurrence | null {
  const start = after.getTime()
  // Orario da parete di partenza, troncato al minuto e avanzato di uno.
  let wall = Math.floor(wallOf(start, timezone) / 60_000) * 60_000 + 60_000
  const limit = new Date(wall).getUTCFullYear() + SEARCH_YEARS

  while (new Date(wall).getUTCFullYear() <= limit) {
    const w = new Date(wall)

    if (!cron.month.has(w.getUTCMonth() + 1)) {
      wall = Date.UTC(w.getUTCFullYear(), w.getUTCMonth() + 1, 1)
      continue
    }
    if (!matchesDay(cron, w)) {
      wall = Date.UTC(w.getUTCFullYear(), w.getUTCMonth(), w.getUTCDate() + 1)
      continue
    }
    if (!cron.hour.has(w.getUTCHours())) {
      wall = Date.UTC(w.getUTCFullYear(), w.getUTCMonth(), w.getUTCDate(), w.getUTCHours() + 1)
      continue
    }
    if (!cron.minute.has(w.getUTCMinutes())) {
      wall += 60_000
      continue
    }

    const { instant, resolution } = resolveWall(wall, timezone)
    if (instant > start) return { at: new Date(instant), resolution }
    wall += 60_000
  }

  return null
}

// -------------------------------------------------------------- intervallo

/** Il limite di `jobs.every_seconds`, che è un `integer` di PostgreSQL. */
const MAX_INTERVAL_SECONDS = 2_147_483_647

/**
 * La prima occorrenza della griglia strettamente successiva ad `after`.
 *
 * La griglia è `epoch + k·intervallo`, in UTC, e non dipende da quando il job è
 * stato creato: è la proprietà su cui si appoggia l'idempotenza di R4, ed è
 * anche ciò che rende l'anteprima onesta. Un `every: 1h` salvato alle 14:37
 * scocca alle 15:00 UTC, non alle 15:37.
 */
function nextInterval(seconds: number, after: Date): Occurrence {
  // Divisione con arrotondamento verso il basso e non verso lo zero: prima
  // dell'epoch i secondi sono negativi e il troncamento darebbe l'occorrenza
  // sbagliata.
  const unix = Math.floor(after.getTime() / 1000)
  const k = Math.floor(unix / seconds)
  return { at: new Date((k + 1) * seconds * 1000), resolution: 'exact' }
}

// ------------------------------------------------------------------ parse

/**
 * Compila una schedulazione, applicando l'esclusività fra le due modalità.
 *
 * L'ordine dei controlli è quello di `schedule.Parse`: prima l'esclusività, poi
 * il fuso — che viene validato **in entrambe le modalità**, anche se a un
 * intervallo non serve. Un refuso nel fuso deve emergere subito, non il giorno
 * in cui quel job viene convertito a `schedule`.
 */
export function compileSchedule(spec: ScheduleSpec): CompiledSchedule | ScheduleError {
  const expression = (spec.expression ?? '').trim()
  const every = spec.everySeconds ?? 0
  const hasCron = expression !== ''
  const hasEvery = every !== 0

  if (hasCron && hasEvery) return { code: 'bothModes' }
  if (!hasCron && !hasEvery) return { code: 'noMode' }

  const timezone = resolveTimezone(spec.timezone)
  if (typeof timezone !== 'string') return timezone

  if (hasEvery) {
    if (!Number.isInteger(every) || every <= 0 || every > MAX_INTERVAL_SECONDS) {
      return { code: 'invalidInterval', value: String(every) }
    }
    return {
      kind: 'interval',
      timezone,
      next: after => nextInterval(every, after),
    }
  }

  const cron = parseCron(expression)
  if (!isCronExpression(cron)) return cron

  return {
    kind: 'cron',
    timezone,
    next: after => nextCron(cron, timezone, after),
  }
}

function isCronExpression(value: CronExpression | ScheduleError): value is CronExpression {
  return 'minute' in value
}

/**
 * Le prossime `count` occorrenze dopo `from`, con la nota su come ciascuna si è
 * risolta.
 *
 * Si ferma prima se l'espressione le esaurisce — `0 0 29 2 *` ne ha una ogni
 * quattro anni e la ricerca guarda avanti otto — e restituisce una lista vuota
 * quando non ne esiste nessuna, che è il caso del 30 febbraio. Una lista vuota
 * è un'informazione, non un guasto: è la risposta che l'utente ha bisogno di
 * vedere prima di salvare un job che non partirà mai.
 */
export function nextOccurrences(
  spec: ScheduleSpec,
  from: Date,
  count: number,
): Occurrence[] | ScheduleError {
  const compiled = compileSchedule(spec)
  if (!('kind' in compiled)) return compiled

  const out: Occurrence[] = []
  let cursor = from
  for (let i = 0; i < count; i += 1) {
    const occurrence = compiled.next(cursor)
    if (occurrence === null) break

    out.push(occurrence)
    cursor = occurrence.at
  }
  return out
}

// ------------------------------------------------------------- durate

/**
 * Legge una durata nella forma dell'API (`10s`, `5m`, `1h`) e la restituisce in
 * secondi.
 *
 * È il sottoinsieme di `time.ParseDuration` che `jobs.FormatDuration` produce, e
 * questa funzione esiste per non doverlo riconoscere in tre posti: l'anteprima,
 * il modulo e i limiti di piano leggono tutti la stessa forma.
 *
 * `null` per ciò che non si legge — compreso `1.5h`, che il backend accetterebbe
 * ma che `jobs.every_seconds` non può memorizzare.
 */
export function parseDurationSeconds(value: string): number | null {
  const trimmed = value.trim()
  const pattern = /^(\d+)(s|m|h)$/.exec(trimmed)
  if (pattern === null) return null

  const amount = Number(pattern[1])
  switch (pattern[2]) {
    case 'h': return amount * 3600
    case 'm': return amount * 60
    default: return amount
  }
}

/**
 * Scrive una durata come la scriverebbe un `cron.yaml`: `1s`, `10s`, `5m`,
 * `1h`. È la stessa scelta di `jobs.FormatDuration`, e per lo stesso motivo —
 * ciò che si legge dall'API si può rimandare indietro senza conversioni.
 */
export function formatDurationSeconds(seconds: number): string {
  if (seconds <= 0) return '0s'
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}
