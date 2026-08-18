/**
 * Il cronjob come lo vedono le schermate: la forma che arriva dall'API, la
 * forma che il modulo modifica, e il passaggio fra le due.
 *
 * ## Tre forme e non una, apposta
 *
 * `JobResponse` è ciò che il backend manda. `JobDraft` è ciò che un modulo
 * modifica — dove un timeout è il testo che si sta scrivendo e può essere
 * `"3o"`, e dove gli header sono una lista ordinata invece di una mappa perché
 * una mappa non si può riordinare né avere una riga vuota in fondo. Il payload è
 * ciò che si rimanda indietro.
 *
 * Tenerle distinte è ciò che permette al modulo di essere permissivo mentre si
 * scrive e severo quando si invia: un campo numerico legato direttamente a un
 * `number` cancella ciò che l'utente sta digitando appena diventa illeggibile,
 * ed è uno dei modi più efficaci di rendere un modulo odioso.
 *
 * ## La validazione qui dentro non decide
 *
 * Vale la regola di `utils/job-contract.ts`, e va ripetuta dove si scrivono i
 * controlli: il giudice è il backend. Qui c'è **solo** ciò che
 * `internal/jobs.Job.Validate` verifica, con gli stessi limiti e gli stessi
 * domini chiusi, per dare una risposta immediata. Niente di più stretto: un
 * modulo che si rifiutasse di inviare per una regola che il server non ha
 * renderebbe irraggiungibile qualcosa che il prodotto sa fare, ed è il difetto
 * peggiore dei due perché non lascia tracce.
 *
 * L'unica eccezione è dichiarata e sta in `validateDraft`: gli header ripetuti.
 */
import type { ApiFieldError } from '~/utils/api'
import type {
  JobAlertChannel,
  JobBackoff,
  JobEnvironment,
  JobMethod,
  JobOverlapPolicy,
} from '~/utils/job-contract'
import type { ScheduleSpec } from '~/utils/schedule'
import {
  byteLength,
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
import { compileSchedule, formatDurationSeconds, parseDurationSeconds } from '~/utils/schedule'

// ------------------------------------------------------- ciò che l'API manda

/** Perché un cambio di piano ha spento il job (R58). */
export interface JobSuspension {
  at: string
  /**
   * `plan_job_limit` — i job attivi superavano il tetto: se ne riaccendono
   * quanti il piano ne consente, e **la scelta è dell'utente**.
   * `plan_resolution` — la schedulazione è più fitta di quanto il piano
   * consenta: va cambiata quella. Non è una questione di posto, e riaccenderne
   * un altro non ne libera.
   *
   * Sono due schermate diverse perché sono due rimedi diversi, ed è l'unica
   * cosa che R58 pretende venga detta all'utente.
   */
  reason: 'plan_job_limit' | 'plan_resolution'
}

/** `httpapi.JobResponse`. Esattamente uno fra `schedule` ed `every`. */
export interface JobResponse {
  id: string
  name: string
  description?: string
  schedule: string | null
  every: string | null
  timezone: string
  environments: string[]
  request: {
    url: string
    method: string
    headers: Record<string, string>
    body: string | null
  }
  timeout: string
  retries: { max: number, backoff: string }
  on_overlap: string
  alerts: { on_failure: string[] }
  enabled: boolean
  /**
   * Valorizzato per i job che vengono da un `cron.yaml` (R13). Il client ne ha
   * bisogno per **sapere in anticipo** che le modifiche vanno fatte nel file,
   * invece di scoprirlo da un 409 dopo aver compilato un modulo.
   */
  repository_id?: string
  suspended?: JobSuspension
  next_run_at: string | null
  archived_at?: string | null
  created_at: string
  updated_at: string
}

export interface JobListResponse {
  jobs: JobResponse[]
  page: { limit: number, next_cursor: string | null }
}

/**
 * `httpapi.SubscriptionResponse`: il piano in forza con i tetti di R15 e i job
 * che un cambio di piano ha spento.
 */
export interface SubscriptionResponse {
  plan: string
  plan_name: string
  status: string
  max_jobs?: number
  active_jobs: number
  /** Risoluzione minima nella forma di `every`: `1s`, `10s`, `1m`. */
  min_interval: string
  log_retention_days: number
  suspended_jobs: { by_job_limit: number, by_resolution: number, total: number }
}

// -------------------------------------------------------- ciò che si modifica

/**
 * Un header nel modulo. È una lista e non una mappa perché una mappa non ha
 * ordine, non ammette una riga vuota in fondo da riempire, e perde la
 * corrispondenza con i campi mentre si scrive il nome.
 */
export interface HeaderDraft {
  name: string
  value: string
}

/**
 * Le due modalità di schedulazione. Nel dominio non esiste un campo «modalità»
 * — sarebbe una terza verità libera di contraddire le altre due, dice
 * `jobs.Job` — ma un modulo ne ha bisogno: un interruttore fra due gruppi di
 * campi è l'unico modo di rendere visibile che sono mutuamente esclusivi.
 * Sparisce nel payload, dove torna a essere «uno dei due valorizzato».
 */
export type ScheduleMode = 'cron' | 'interval'

export interface JobDraft {
  name: string
  description: string

  mode: ScheduleMode
  /** Espressione cron, quando `mode` è `cron`. */
  schedule: string
  /** Quantità e unità dell'intervallo, quando `mode` è `interval`. */
  everyAmount: string
  everyUnit: 's' | 'm' | 'h'
  timezone: string

  environments: JobEnvironment[]

  url: string
  method: JobMethod
  headers: HeaderDraft[]
  body: string

  /** Testo, non numero: vedi la nota in testa al file. */
  timeoutSeconds: string
  maxRetries: string
  retryBackoff: JobBackoff
  overlapPolicy: JobOverlapPolicy
  alertOnFailure: JobAlertChannel[]
  enabled: boolean
}

/** Un job nuovo, con i valori predefiniti di `jobs.NewJob()` già in vista. */
export function emptyDraft(): JobDraft {
  return {
    name: '',
    description: '',
    mode: 'cron',
    schedule: '',
    everyAmount: '',
    everyUnit: 'm',
    timezone: JOB_DEFAULTS.timezone,
    environments: [...JOB_DEFAULTS.environments],
    url: '',
    method: JOB_DEFAULTS.method,
    headers: [],
    body: '',
    timeoutSeconds: String(JOB_DEFAULTS.timeoutSeconds),
    maxRetries: String(JOB_DEFAULTS.maxRetries),
    retryBackoff: JOB_DEFAULTS.retryBackoff,
    overlapPolicy: JOB_DEFAULTS.overlapPolicy,
    alertOnFailure: [...JOB_DEFAULTS.alertOnFailure],
    enabled: JOB_DEFAULTS.enabled,
  }
}

/**
 * Il job letto dall'API, nella forma che il modulo modifica.
 *
 * I valori fuori dai domini chiusi non vengono scartati: un metodo che il
 * browser non conosce è un backend più nuovo di questo bundle, e sostituirlo con
 * `POST` cambierebbe il job di qualcun altro senza dirlo. Restano come sono, e
 * il `<select>` li mostra fra le proprie opzioni finché non si sceglie altro.
 */
export function draftFromJob(job: JobResponse): JobDraft {
  const every = job.every === null ? null : parseDurationSeconds(job.every)
  const timeout = parseDurationSeconds(job.timeout)

  return {
    name: job.name,
    description: job.description ?? '',
    mode: job.every === null ? 'cron' : 'interval',
    schedule: job.schedule ?? '',
    ...intervalFields(every),
    timezone: job.timezone,
    environments: job.environments as JobEnvironment[],
    url: job.request.url,
    method: job.request.method as JobMethod,
    headers: Object.entries(job.request.headers ?? {}).map(([name, value]) => ({ name, value })),
    body: job.request.body ?? '',
    timeoutSeconds: timeout === null ? job.timeout : String(timeout),
    maxRetries: String(job.retries.max),
    retryBackoff: job.retries.backoff as JobBackoff,
    overlapPolicy: job.on_overlap as JobOverlapPolicy,
    alertOnFailure: job.alerts.on_failure as JobAlertChannel[],
    enabled: job.enabled,
  }
}

/**
 * L'intervallo nella coppia numero + unità più leggibile che lo rappresenti.
 *
 * `3600` diventa «1 ora» e non «3600 secondi»: sono lo stesso valore, ma il
 * secondo si rilegge male e invita a cambiarlo in «3601» invece che in «2 ore».
 */
function intervalFields(seconds: number | null): Pick<JobDraft, 'everyAmount' | 'everyUnit'> {
  if (seconds === null || seconds <= 0) return { everyAmount: '', everyUnit: 'm' }
  if (seconds % 3600 === 0) return { everyAmount: String(seconds / 3600), everyUnit: 'h' }
  if (seconds % 60 === 0) return { everyAmount: String(seconds / 60), everyUnit: 'm' }
  return { everyAmount: String(seconds), everyUnit: 's' }
}

/** L'intervallo del modulo in secondi; `null` se non si legge come intero. */
export function draftIntervalSeconds(draft: JobDraft): number | null {
  const raw = draft.everyAmount.trim()
  if (!/^\d+$/.test(raw)) return null

  const amount = Number(raw)
  if (amount <= 0) return null

  const factor = draft.everyUnit === 'h' ? 3600 : draft.everyUnit === 'm' ? 60 : 1
  return amount * factor
}

/**
 * La schedulazione del modulo nella forma che l'anteprima accetta.
 *
 * È la stessa `Spec` di `jobs.Job.Spec()`: espressione **oppure** intervallo,
 * più il fuso, che vale in entrambe le modalità.
 */
export function draftSchedule(draft: JobDraft): ScheduleSpec {
  if (draft.mode === 'interval') {
    return { everySeconds: draftIntervalSeconds(draft) ?? 0, timezone: draft.timezone }
  }
  return { expression: draft.schedule, timezone: draft.timezone }
}

/** Gli header del modulo nella mappa che l'API accetta. */
export function draftHeaders(draft: JobDraft): Record<string, string> {
  const headers: Record<string, string> = {}
  for (const header of draft.headers) {
    const name = header.name.trim()
    if (name !== '') headers[name] = header.value
  }
  return headers
}

/**
 * Il corpo di `POST /jobs` e `PATCH /jobs/{id}`.
 *
 * Ogni campo è presente, anche quello che non è cambiato: il contratto è «ciò
 * che non mandi non cambia», e mandare tutto è equivalente a mandare la
 * differenza — con in più la proprietà che due schede aperte sullo stesso job
 * non producono una fusione silenziosa di due modifiche parziali.
 *
 * Le due modalità si escludono a vicenda e il `null` esplicito è ciò che le
 * commuta: `optional[T]` in `internal/httpapi` distingue «campo assente» da
 * «campo a null», e senza il secondo non ci sarebbe modo di dismettere una
 * modalità.
 */
export function jobPayload(draft: JobDraft): Record<string, unknown> {
  const every = draftIntervalSeconds(draft)
  const timeout = draft.timeoutSeconds.trim()

  return {
    name: draft.name.trim(),
    description: draft.description.trim(),
    schedule: draft.mode === 'cron' ? draft.schedule.trim() : null,
    every: draft.mode === 'interval' && every !== null ? formatDurationSeconds(every) : null,
    timezone: draft.timezone.trim(),
    environments: draft.environments,
    request: {
      url: draft.url.trim(),
      method: draft.method,
      headers: draftHeaders(draft),
      body: draft.body === '' ? null : draft.body,
    },
    // La durata viaggia nella forma di `cron.yaml` (`30s`), non come numero: è
    // la stessa scelta del resto dell'API, e ciò che si legge si può rimandare
    // indietro senza conversioni.
    timeout: `${timeout}s`,
    retries: { max: Number(draft.maxRetries.trim()), backoff: draft.retryBackoff },
    on_overlap: draft.overlapPolicy,
    alerts: { on_failure: draft.alertOnFailure },
    enabled: draft.enabled,
  }
}

// -------------------------------------------------------------- validazione

/**
 * I campi, nella notazione a punti del corpo JSON — la stessa di
 * `FieldErrorBody.field`.
 *
 * Usare il vocabolario del backend invece di uno nostro non è pedanteria: è ciò
 * che permette a un rifiuto arrivato dal server di finire accanto al campo
 * giusto senza una tabella di traduzione in mezzo, che è il posto in cui
 * nascono i difetti.
 */
export type JobField
  = | 'name' | 'description' | 'schedule' | 'every' | 'timezone' | 'environments'
    | 'request.url' | 'request.method' | 'request.headers' | 'request.body'
    | 'timeout' | 'retries.max' | 'retries.backoff' | 'on_overlap' | 'alerts.on_failure'

/**
 * Perché un campo è stato rifiutato. Sono **chiavi di testo**, non frasi: il
 * messaggio sta in `content.jobs.errors`, in cinque lingue.
 */
export type JobIssueCode
  = | 'required' | 'tooLong' | 'invalidName' | 'invalidUrl' | 'unsupportedScheme'
    | 'invalidHeaderName' | 'reservedHeader' | 'duplicateHeader' | 'headerNewline'
    | 'tooManyHeaders' | 'headerTooLong' | 'bodyTooLong'
    | 'timeoutRange' | 'timeoutWhole' | 'retriesRange' | 'environmentsRequired'
    | 'scheduleRequired' | 'scheduleConflict' | 'scheduleMacro' | 'scheduleFieldCount'
    | 'scheduleField' | 'unknownTimezone' | 'localTimezone' | 'invalidInterval'
    | 'targetNotAllowed' | 'nameTaken' | 'rejected'

export interface JobIssue {
  field: JobField
  code: JobIssueCode
  /** Il limite superato, per i messaggi che lo nominano. */
  limit?: number
  /** Il valore che non va — un nome di header, un campo dell'espressione. */
  value?: string
}

/**
 * Verifica il modulo con le regole che `internal/jobs.Job.Validate` applica, e
 * **soltanto** con quelle.
 *
 * L'ordine ricalca quello del backend — identità, schedulazione, ambienti,
 * bersaglio, esecuzione — perché è l'ordine in cui i campi stanno sullo schermo,
 * e perché così i due elenchi si leggono uno accanto all'altro.
 *
 * ## L'unica regola che il backend non ha, e perché c'è
 *
 * Gli **header ripetuti**. Nel corpo JSON gli header sono una mappa, quindi due
 * righe con lo stesso nome non arrivano nemmeno al server: una delle due sparisce
 * durante la conversione. Non è una validazione più stretta della richiesta — la
 * richiesta che ne risulta il server la accetta — è il rifiuto di **inviare
 * qualcosa di diverso da ciò che è scritto sullo schermo**, che è l'unico caso in
 * cui tacere sarebbe peggio che fermarsi.
 */
export function validateDraft(draft: JobDraft): JobIssue[] {
  const issues: JobIssue[] = []
  const add = (field: JobField, code: JobIssueCode, extra: Partial<JobIssue> = {}): void => {
    issues.push({ field, code, ...extra })
  }

  // ---- identità
  const name = draft.name.trim()
  if (name === '') add('name', 'required')
  else if (name.length > JOB_LIMITS.maxNameLength) add('name', 'tooLong', { limit: JOB_LIMITS.maxNameLength })
  else if (!JOB_NAME_PATTERN.test(name)) add('name', 'invalidName')

  if (draft.description.trim().length > JOB_LIMITS.maxDescriptionLength) {
    add('description', 'tooLong', { limit: JOB_LIMITS.maxDescriptionLength })
  }

  // ---- schedulazione
  validateSchedule(draft, add)

  // ---- ambienti
  if (draft.environments.length === 0) add('environments', 'environmentsRequired')

  // ---- bersaglio
  validateTarget(draft, add)

  // ---- esecuzione
  const timeout = draft.timeoutSeconds.trim()
  if (!/^\d+$/.test(timeout)) {
    add('timeout', 'timeoutWhole')
  }
  else {
    const seconds = Number(timeout)
    if (seconds < JOB_LIMITS.minTimeoutSeconds || seconds > JOB_LIMITS.maxTimeoutSeconds) {
      add('timeout', 'timeoutRange')
    }
  }

  const retries = draft.maxRetries.trim()
  if (!/^\d+$/.test(retries) || Number(retries) > JOB_LIMITS.maxRetries) {
    add('retries.max', 'retriesRange', { limit: JOB_LIMITS.maxRetries })
  }

  return issues
}

type Adder = (field: JobField, code: JobIssueCode, extra?: Partial<JobIssue>) => void

function validateSchedule(draft: JobDraft, add: Adder): void {
  if (draft.mode === 'interval') {
    if (draftIntervalSeconds(draft) === null) add('every', 'invalidInterval')
  }
  else if (draft.schedule.trim() === '') {
    add('schedule', 'scheduleRequired')
  }

  /*
   * Il resto lo dice il compilatore della schedulazione, che è il porto del
   * validatore del backend: riscrivere qui il controllo dei cinque campi
   * significherebbe due parser che divergono, ed è lo stesso argomento per cui
   * `jobs.Job.validateSchedule` delega a `internal/schedule` invece di
   * riscriverne le regole.
   */
  const compiled = compileSchedule(draftSchedule(draft))
  if ('kind' in compiled) return

  switch (compiled.code) {
    case 'noMode':
      // Già detto sopra, con il campo giusto: qui sarebbe un doppione.
      break
    case 'bothModes':
      add('schedule', 'scheduleConflict')
      break
    case 'unknownTimezone':
      add('timezone', 'unknownTimezone', { value: compiled.value })
      break
    case 'localTimezone':
      add('timezone', 'localTimezone')
      break
    case 'macro':
      add('schedule', 'scheduleMacro')
      break
    case 'fieldCount':
      add('schedule', 'scheduleFieldCount')
      break
    case 'invalidField':
      add('schedule', 'scheduleField', { value: compiled.value })
      break
    case 'invalidInterval':
      add('every', 'invalidInterval')
      break
  }
}

function validateTarget(draft: JobDraft, add: Adder): void {
  const url = draft.url.trim()
  if (url === '') {
    add('request.url', 'required')
  }
  else if (url.length > JOB_LIMITS.maxUrlLength) {
    add('request.url', 'tooLong', { limit: JOB_LIMITS.maxUrlLength })
  }
  else {
    /*
     * `URL` è più severo di `url.Parse` di Go, che accetta anche indirizzi
     * relativi: qui un valore che non si legge affatto è un errore di formato,
     * e uno schema diverso da http/https è `jobs_url_scheme_check` — Postqron
     * non esegue comandi né container, solo HTTP (SPEC §10).
     *
     * Ciò che questo controllo **non** è: il blocco dei bersagli (R38). Quello
     * vive dov'è l'unica cosa che conta, cioè l'apertura della connessione, e
     * nessun controllo fatto nel browser può anticiparlo.
     */
    let parsed: URL | null = null
    try {
      parsed = new URL(url)
    }
    catch {
      parsed = null
    }
    if (parsed === null || parsed.host === '') add('request.url', 'invalidUrl')
    else if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      add('request.url', 'unsupportedScheme')
    }
  }

  const named = draft.headers.filter(header => header.name.trim() !== '')
  if (named.length > JOB_LIMITS.maxHeaders) {
    add('request.headers', 'tooManyHeaders', { limit: JOB_LIMITS.maxHeaders })
  }

  const seen = new Set<string>()
  for (const header of named) {
    const name = header.name.trim()
    const lower = name.toLowerCase()

    if (!HEADER_NAME_PATTERN.test(name)) add('request.headers', 'invalidHeaderName', { value: name })
    else if (name.length > JOB_LIMITS.maxHeaderNameLength) {
      add('request.headers', 'headerTooLong', { value: name, limit: JOB_LIMITS.maxHeaderNameLength })
    }
    else if (RESERVED_HEADERS.includes(lower)) {
      add('request.headers', 'reservedHeader', { value: name })
    }
    else if (header.value.length > JOB_LIMITS.maxHeaderValueLength) {
      add('request.headers', 'headerTooLong', { value: name, limit: JOB_LIMITS.maxHeaderValueLength })
    }
    else if (/[\r\n]/.test(header.value)) {
      // Un a capo in un valore è una richiesta di iniettare header aggiuntivi
      // nella chiamata che faremmo noi, dal nostro IP.
      add('request.headers', 'headerNewline', { value: name })
    }

    if (seen.has(lower)) add('request.headers', 'duplicateHeader', { value: name })
    seen.add(lower)
  }

  if (byteLength(draft.body) > JOB_LIMITS.maxBodyLength) {
    add('request.body', 'bodyTooLong', { limit: JOB_LIMITS.maxBodyLength })
  }
}

// ------------------------------------------------- i rifiuti che arrivano dal server

/**
 * I codici che `internal/jobs` ancora ai campi, tradotti nel vocabolario di
 * `content.jobs.errors`.
 *
 * Serve perché i due elenchi non coincidono, e non devono: il client distingue
 * più casi sulla schedulazione — sa dire *quale* campo dell'espressione non va —
 * mentre il backend ne ha uno solo con il dettaglio dentro un messaggio che non
 * si può mostrare, perché è in italiano (SPEC §8-bis).
 *
 * Ciò che non è in tabella ricade su `rejected`, che dice «il server ha
 * rifiutato questo campo» senza inventarsi il motivo. È una frase debole, e va
 * bene che lo sia: comparirà solo quando il backend avrà un codice che questo
 * bundle non conosce, cioè quando è più nuovo di lui.
 */
const SERVER_CODES: Record<string, JobIssueCode> = {
  required: 'required',
  too_long: 'tooLong',
  too_many: 'tooManyHeaders',
  invalid_name: 'invalidHeaderName',
  reserved_name: 'reservedHeader',
  duplicate: 'duplicateHeader',
  invalid_value: 'headerNewline',
  unsupported_scheme: 'unsupportedScheme',
  target_not_allowed: 'targetNotAllowed',
  schedule_required: 'scheduleRequired',
  schedule_conflict: 'scheduleConflict',
  invalid_schedule: 'scheduleField',
  invalid_interval: 'invalidInterval',
  out_of_range: 'timeoutRange',
}

const JOB_FIELDS: readonly JobField[] = [
  'name', 'description', 'schedule', 'every', 'timezone', 'environments',
  'request.url', 'request.method', 'request.headers', 'request.body',
  'timeout', 'retries.max', 'retries.backoff', 'on_overlap', 'alerts.on_failure',
]

/**
 * I `details[]` di un rifiuto del backend, nella forma che il modulo mostra.
 *
 * Un campo che questo bundle non conosce viene scartato: mostrarlo senza sapere
 * a quale casella appartiene significherebbe un messaggio che galleggia in fondo
 * al modulo, e chi lo legge non saprebbe comunque cosa correggere. Il rifiuto
 * complessivo resta visibile in testa al modulo.
 */
export function issuesFromServer(details: readonly ApiFieldError[]): JobIssue[] {
  return details.flatMap((detail) => {
    if (!JOB_FIELDS.includes(detail.field as JobField)) return []
    return [{
      field: detail.field as JobField,
      code: SERVER_CODES[detail.code] ?? 'rejected',
    }]
  })
}

/** Il primo problema di un campo, che è quello da mostrargli accanto. */
export function issueFor(issues: readonly JobIssue[], field: JobField): JobIssue | null {
  return issues.find(issue => issue.field === field) ?? null
}

// ------------------------------------------------------------------ etichette

/** Riconosce i valori che il `<select>` conosce; il resto resta com'è. */
export function isKnownMethod(value: string): value is JobMethod {
  return (JOB_METHODS as readonly string[]).includes(value)
}

export function isKnownBackoff(value: string): value is JobBackoff {
  return (JOB_BACKOFFS as readonly string[]).includes(value)
}

export function isKnownOverlap(value: string): value is JobOverlapPolicy {
  return (JOB_OVERLAP_POLICIES as readonly string[]).includes(value)
}

export function isKnownChannel(value: string): value is JobAlertChannel {
  return (JOB_ALERT_CHANNELS as readonly string[]).includes(value)
}

export function isKnownEnvironment(value: string): value is JobEnvironment {
  return (JOB_ENVIRONMENTS as readonly string[]).includes(value)
}
