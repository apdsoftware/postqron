/**
 * Il contratto di forma di un cronjob: i limiti, i domini chiusi e i valori
 * predefiniti che `services/api/internal/jobs` impone.
 *
 * ## Perché una copia esiste, e cosa la tiene onesta
 *
 * La validazione vera sta nel backend e nel database — tre copie della stessa
 * verità, che un test di `internal/jobspg` già tiene allineate. Questa è la
 * quarta, e serve a una cosa sola: **dare una risposta immediata**. Scoprire che
 * un nome è troppo lungo dopo un viaggio verso il server è attrito senza
 * contropartita, e su un modulo con quindici campi è attrito ripetuto.
 *
 * Ciò che questa copia **non** fa è decidere. La regola sta scritta in due posti
 * e il giudice resta uno: un modulo che si rifiutasse di inviare per una regola
 * che il server non ha sarebbe un difetto tanto quanto il contrario, e più
 * insidioso, perché renderebbe irraggiungibile qualcosa che il prodotto sa fare.
 * Per questo qui non c'è **niente** che il backend non abbia: nessun limite più
 * stretto, nessun campo obbligatorio in più, nessun formato inventato.
 *
 * ## Come resta allineata
 *
 * `test/job-contract.test.ts` legge i sorgenti Go — `internal/jobs/validate.go`
 * e `internal/jobs/jobs.go` — ed estrae da lì ogni numero, ogni espressione
 * regolare e ogni dominio chiuso di questo file, confrontandoli uno per uno.
 * Cambiare `MaxTimeout` in Go e non qui fa fallire `make ci` nominando la
 * costante; aggiungere un metodo HTTP ammesso e non elencarlo qui, idem.
 *
 * È la stessa forma di presidio che il backend usa fra sé e PostgreSQL, e per la
 * stessa ragione: la duplicazione non si toglie — le due parti girano su
 * macchine diverse e in linguaggi diversi — ma si può rendere impossibile
 * lasciarla divergere in silenzio.
 *
 * ## Cosa resta al server, e per scelta
 *
 * - Il **blocco dei bersagli** (R38): un URL che risolve internamente si
 *   riconosce solo aprendo la connessione, e il messaggio di rifiuto è
 *   deliberatamente vago perché non diventi una scansione della nostra rete.
 * - I **limiti di piano** (R15): stanno in `plans`, non qui. Il client li
 *   riceve da `GET /billing/subscription` e li **mostra**; chi decide è il
 *   backend.
 * - L'**unicità del nome**: il database la conosce, il browser no.
 */

/**
 * I limiti di forma, in unità del client: caratteri, byte, secondi.
 *
 * Ogni chiave nomina la costante Go da cui viene, perché è quella che il test
 * di allineamento confronta e quella che va cercata se un numero non torna.
 */
export const JOB_LIMITS = {
  /** `jobs.MaxNameLength`, che è anche il tetto di `jobs_name_format_check`. */
  maxNameLength: 100,
  /** `jobs.MaxDescriptionLength`. */
  maxDescriptionLength: 2000,
  /** `jobs.MaxURLLength`. */
  maxUrlLength: 2048,
  /** `jobs.MaxHeaders`. */
  maxHeaders: 50,
  /** `jobs.MaxHeaderNameLength`. */
  maxHeaderNameLength: 128,
  /** `jobs.MaxHeaderValueLength`. */
  maxHeaderValueLength: 4096,
  /** `jobs.MaxBodyLength`, in byte e non in caratteri: vedi `byteLength()`. */
  maxBodyLength: 16384,
  /** `jobs.MinTimeout`, in secondi. */
  minTimeoutSeconds: 1,
  /** `jobs.MaxTimeout`, in secondi. */
  maxTimeoutSeconds: 300,
  /** `jobs.MaxRetriesAllowed`. */
  maxRetries: 10,
} as const

/**
 * `jobs.nameFormat`, che a sua volta riproduce `jobs_name_format_check` della
 * migrazione 0005.
 *
 * Il nome è l'identità stabile del job (SPEC §9): è la chiave su cui la
 * riconciliazione di un `cron.yaml` decide se creare, aggiornare o disattivare.
 * Rinominare un job equivale a cancellarlo e crearne un altro, ed è il motivo
 * per cui il formato è stretto.
 */
export const JOB_NAME_PATTERN = /^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$/

/** `jobs.headerNameFormat`: il `token` di RFC 9110 §5.6.2. */
export const HEADER_NAME_PATTERN = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

/**
 * `jobs.reservedHeaders`: gli header che il target non può dichiarare perché li
 * decide l'esecutore.
 *
 * `Host` cambierebbe il bersaglio effettivo della richiesta senza cambiare
 * l'URL — la forma di aggiramento che il blocco SSRF deve poter escludere
 * guardando l'URL. Gli altri li calcola il client HTTP: sovrascriverli produce
 * richieste malformate.
 */
export const RESERVED_HEADERS: readonly string[] = [
  'host', 'content-length', 'connection', 'transfer-encoding',
  'upgrade', 'keep-alive', 'proxy-authorization', 'te', 'trailer',
]

/** `jobs.Methods`. Postqron esegue solo chiamate HTTP (SPEC §10). */
export const JOB_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const
export type JobMethod = (typeof JOB_METHODS)[number]

/** `jobs.Backoffs`: la politica di attesa fra due tentativi (R5). */
export const JOB_BACKOFFS = ['exponential', 'linear', 'fixed'] as const
export type JobBackoff = (typeof JOB_BACKOFFS)[number]

/**
 * `jobs.OverlapPolicies`: cosa fare quando un'occorrenza scatta mentre la
 * precedente è ancora in corso (R41). Con la risoluzione al secondo non è un
 * caso raro, è la norma.
 */
export const JOB_OVERLAP_POLICIES = ['skip', 'queue', 'allow'] as const
export type JobOverlapPolicy = (typeof JOB_OVERLAP_POLICIES)[number]

/** `jobs.AlertChannels`: i canali di avviso su fallimento (R21, R29). */
export const JOB_ALERT_CHANNELS = ['email', 'slack', 'discord', 'webhook'] as const
export type JobAlertChannel = (typeof JOB_ALERT_CHANNELS)[number]

/**
 * `jobs.Environments`, **nell'ordine di dichiarazione del tipo enumerato**
 * (migrazione 0001): `staging` viene prima di `production`. L'ordine non è
 * decorativo — vedi `jobs.EnvironmentRank` — e riprodurlo qui costa nulla.
 */
export const JOB_ENVIRONMENTS = ['staging', 'production'] as const
export type JobEnvironment = (typeof JOB_ENVIRONMENTS)[number]

/** `jobs.ExecutionStatuses`: lo stato di un tentativo (R6). */
export const EXECUTION_STATUSES = [
  'pending', 'running', 'succeeded', 'failed', 'timed_out', 'skipped',
] as const
export type ExecutionStatus = (typeof EXECUTION_STATUSES)[number]

/**
 * I valori con cui nasce un job, cioè quelli di `jobs.NewJob()`.
 *
 * Servono al modulo di creazione, che deve **mostrarli** invece di lasciare
 * campi vuoti: un timeout che il database riempirà da sé è un timeout che
 * l'utente scopre solo rileggendo il job dopo averlo salvato. Il backend li
 * applica comunque a ciò che non riceve — qui non si aggirano, si anticipano.
 */
export const JOB_DEFAULTS = {
  timezone: 'UTC',
  environments: ['production'] as readonly JobEnvironment[],
  method: 'POST' as JobMethod,
  timeoutSeconds: 30,
  maxRetries: 3,
  retryBackoff: 'exponential' as JobBackoff,
  /**
   * `jobs.DefaultOverlapPolicy`. È `skip` perché è l'unica delle tre che non fa
   * danni a un job di cui non si sa niente: `allow` chiama due volte insieme un
   * bersaglio che potrebbe emettere una fattura per chiamata, `queue` accumula
   * un arretrato illimitato quando il job è più lento del proprio intervallo.
   */
  overlapPolicy: 'skip' as JobOverlapPolicy,
  alertOnFailure: ['email'] as readonly JobAlertChannel[],
  enabled: true,
} as const

/**
 * Lunghezza in **byte** di una stringa, che è l'unità di `jobs.MaxBodyLength`.
 *
 * `String.length` conta unità UTF-16: un corpo JSON pieno di accenti o di
 * emoji passerebbe il controllo qui e verrebbe rifiutato dal server, che è
 * esattamente la direzione di divergenza da evitare.
 */
export function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}
