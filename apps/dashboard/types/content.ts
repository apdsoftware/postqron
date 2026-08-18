/**
 * Forma dei testi della dashboard.
 *
 * I componenti non conoscono i testi: li leggono da qui attraverso
 * `useLocale()`. Un componente che contiene una frase è un difetto, perché non
 * è traducibile (SPEC §8-bis); `test/no-strings.test.ts` verifica che non ne
 * rientri nessuna.
 *
 * Ogni lingua compila `DashboardContent` per intero in `content/<codice>.ts`.
 * L'inglese è la lingua sorgente: si scrive lì e si traduce da lì.
 *
 * La struttura segue l'interfaccia, non le pagine: `shell` è ciò che si vede su
 * ogni schermata, `status` è il vocabolario degli stati che ogni vista può
 * assumere, `home` e `notFound` sono due schermate. Le sezioni della dashboard
 * vera — job, esecuzioni, chiavi API — aggiungeranno una chiave ciascuna con le
 * issue che le portano, più una voce in `shell.nav`.
 */
import type { ApiErrorKind } from '~/utils/api'
import type {
  JobAlertChannel,
  JobBackoff,
  JobEnvironment,
  JobOverlapPolicy,
} from '~/utils/job-contract'
import type { JobIssueCode } from '~/utils/jobs'
import type { NavId } from '~/utils/navigation'

/** Testi presenti su ogni schermata, indipendenti dalla rotta. */
export interface ShellContent {
  /**
   * Nome accessibile del selettore di lingua.
   *
   * Il selettore mostra solo i nomi delle cinque lingue: senza un'etichetta chi
   * usa uno screen reader sente un elenco di parole senza sapere cosa scelgono.
   */
  languageLabel: string

  /**
   * Collegamento che salta la navigazione e porta al contenuto (R54, WCAG 2.2
   * 2.4.1). Chi naviga da tastiera altrimenti attraversa barra superiore e
   * barra laterale a ogni cambio di pagina prima di arrivare a leggere.
   */
  skipToContent: string

  /** Nome accessibile della navigazione principale. */
  navigationLabel: string

  /** Etichette del pulsante che apre e chiude la barra laterale sul telefono. */
  openNavigation: string
  closeNavigation: string

  /**
   * Nomi delle sezioni, uno per voce del registro in `utils/navigation.ts`.
   *
   * `Record<NavId, string>` è la garanzia strutturale: una sezione aggiunta al
   * registro senza il testo non compila, in nessuna delle cinque lingue.
   */
  nav: Record<NavId, string>

  /**
   * Etichette dell'interruttore del tema. Dicono **dove si va**, non dove si è:
   * il pulsante è un'azione, e «tema scuro» su un'interfaccia già scura
   * sembrerebbe uno stato.
   */
  toLightTheme: string
  toDarkTheme: string

  /** Menu dell'utente collegato, in fondo alla barra superiore. */
  account: AccountMenuContent
}

/** Menu dell'utente collegato. */
export interface AccountMenuContent {
  /**
   * Nome accessibile del pulsante che apre il menu. Non è l'email: quella si
   * legge dentro, e un pulsante che si chiama come il proprio contenuto non
   * dice a cosa serve.
   */
  open: string
  /** Intestazione che precede l'indirizzo dell'utente. */
  signedInAs: string
  /** Comando che chiude la sessione. */
  signOut: string
}

/**
 * Vocabolario degli stati di una vista (R56).
 *
 * Sta in una chiave sua e non dentro ogni schermata perché le frasi sono le
 * stesse ovunque: «non si è riusciti a caricare» non cambia a seconda che si
 * stessero caricando i job o le esecuzioni, e duplicarla per vista significa
 * cinque traduzioni in più per ogni sezione nuova.
 */
export interface StatusContent {
  /** Annuncio del caricamento per i lettori di schermo. */
  loading: string
  /** Titolo del riquadro d'errore. */
  errorTitle: string
  /** Pulsante che rifà la richiesta. */
  retry: string
  /**
   * Un messaggio per categoria di guasto (`ApiErrorKind`), perché è la
   * distinzione su cui cambia cosa può fare l'utente.
   */
  errors: Record<ApiErrorKind, string>
}

/** Schermata iniziale. */
export interface HomeContent {
  title: string
  intro: string
  backendTitle: string
  /** Etichetta dell'indirizzo dell'API; il valore accanto non è tradotto. */
  apiBaseLabel: string
  /** Etichette dei tre valori restituiti dall'health check. */
  statusLabel: string
  environmentLabel: string
  versionLabel: string
  /** Pulsante di verifica. */
  check: string
}

/** Indirizzo che non corrisponde a nessuna schermata. */
export interface NotFoundContent {
  title: string
  intro: string
  /** Collegamento di rientro alla panoramica. */
  back: string
}

/**
 * Accesso e registrazione (R14).
 *
 * Le due schermate condividono una sola chiave perché condividono quasi tutto:
 * gli stessi campi, gli stessi errori, e il rimando reciproco. Separarle
 * significherebbe tradurre due volte «Email» e «Password» in cinque lingue.
 */
export interface AuthContent {
  signIn: SignInContent
  signUp: SignUpContent
  /** Etichette dei campi, condivise dai due moduli. */
  fields: AuthFieldsContent
  /** Messaggi di rifiuto, condivisi dai due moduli. */
  errors: AuthErrorsContent
}

export interface SignInContent {
  title: string
  /** Pulsante che invia il modulo. */
  submit: string
  /** Testo mentre la richiesta è in volo. */
  submitting: string
  /** Invito a registrarsi, per chi non ha un account. */
  noAccount: string
  noAccountLink: string
  /**
   * Avviso mostrato quando si arriva qui per una sessione finita da sé, non per
   * un accesso volontario. Dice **cosa è successo**, non «errore»: non ha
   * sbagliato nessuno.
   */
  interrupted: string
  /**
   * Avviso mostrato quando si arriva qui da una schermata protetta: dopo
   * l'accesso ci si torna. Serve a spiegare perché l'indirizzo aperto non è
   * quello che si sta guardando.
   */
  returningTo: string
}

export interface SignUpContent {
  title: string
  submit: string
  submitting: string
  /** Invito ad accedere, per chi un account ce l'ha già. */
  haveAccount: string
  haveAccountLink: string
  /**
   * Esito della registrazione. **È lo stesso che l'indirizzo fosse libero o già
   * registrato**, e la vaghezza è deliberata: il backend risponde 202 nei due
   * casi proprio per non dire quali indirizzi ha (`acceptedResponse` in
   * `internal/httpapi/auth.go`). Un titolo tipo «Account creato» annullerebbe
   * quel lavoro dal lato dell'interfaccia.
   */
  acceptedTitle: string
  acceptedBody: string
  /** Collegamento all'accesso dalla schermata di esito. */
  acceptedSignIn: string
}

export interface AuthFieldsContent {
  email: string
  password: string
  fullName: string
  /**
   * Requisito della password, mostrato **prima** di scrivere e non come errore
   * dopo. Il numero dentro la frase è `MinPasswordLength` del backend, che resta
   * l'unico giudice: qui si ripete solo per non far scoprire la regola con un
   * viaggio verso il server.
   */
  passwordHint: string
}

export interface AuthErrorsContent {
  /**
   * Credenziali rifiutate.
   *
   * **Uno solo, e volutamente vago fra «utente inesistente» e «password
   * sbagliata».** Il backend si difende dall'enumerazione degli account fino a
   * pareggiare i *tempi* di risposta dei due casi (verifica una password finta
   * quando l'utente non c'è); due messaggi diversi qui vanificherebbero tutto
   * quel lavoro con una frase.
   */
  credentials: string
  /** Troppi tentativi: 429. Non dice se l'account esiste, perché il limite scatta comunque. */
  tooManyAttempts: string
  /** Account sospeso: 403, e arriva solo dopo una password corretta. */
  suspended: string
  /** Indirizzo email malformato: 400 `invalid_email`. */
  invalidEmail: string
  /** Password che non rispetta la policy: 400 `weak_password`. */
  weakPassword: string
  /** Qualunque altro rifiuto, compresi i guasti di rete e del server. */
  unexpected: string
  /** Campo lasciato vuoto, verificato dal browser prima di partire. */
  required: string
}

/**
 * Cronjob: elenco, modulo e anteprima (SPEC §4.2, R1, R15, R41, R56, R58).
 *
 * ## I segnaposti
 *
 * Alcune frasi contengono `{nome}`, riempito da `fill()` di `utils/text.ts`.
 * Sono i punti in cui la frase deve nominare un numero che **arriva dal
 * backend** — il tetto del piano, la risoluzione minima, il fuso del job — e
 * spezzarle in due metà con il valore in mezzo produrrebbe traduzioni
 * impossibili: in tedesco quel valore sta in un altro punto della frase.
 *
 * Nessuno di questi numeri è scritto qui dentro: la frase dice *dove* va il
 * valore, non quale sia.
 */
export interface JobsContent {
  list: JobsListContent
  form: JobFormContent
  fields: JobFieldsContent
  options: JobOptionsContent
  preview: SchedulePreviewContent
  plan: PlanContent
  state: JobStateContent
  /**
   * Un messaggio per motivo di rifiuto di un campo.
   *
   * `Record<JobIssueCode, string>` è la garanzia strutturale: un motivo nuovo —
   * introdotto perché il backend ne ha introdotto uno — non compila finché non
   * ha una frase in tutte e cinque le lingue. È lo stesso legame che
   * `shell.nav` ha con il registro della navigazione.
   */
  errors: Record<JobIssueCode, string>
}

/** Elenco dei cronjob. */
export interface JobsListContent {
  title: string
  intro: string
  /** Comando che porta al modulo di creazione. */
  create: string
  /**
   * Stato vuoto (R56). Dice cosa manca **e cosa fare**: «nessun risultato» non
   * aiuta nessuno, e questa è la prima schermata del percorso di R55.
   */
  empty: string
  emptyHint: string

  columnName: string
  columnSchedule: string
  columnNextRun: string
  columnState: string
  columnActions: string

  /**
   * Prossima esecuzione non ancora calcolata. Non è un guasto: `next_run_at` lo
   * scrive lo scheduler, e un job appena creato nasce senza (migrazione 0010).
   */
  nextRunPending: string
  /** Il job non ne ha una perché è fermo. */
  nextRunNone: string

  edit: string
  runNow: string
  running: string
  /** Esito del trigger manuale: 202, quindi «registrata», non «eseguita». */
  runQueued: string
  pause: string
  resume: string
  delete: string
  deleteTitle: string
  deleteBody: string
  deleteConfirm: string
  deleteCancel: string
}

/** Modulo di creazione e modifica. */
export interface JobFormContent {
  createTitle: string
  editTitle: string
  save: string
  saving: string
  /** Conferma che la modifica è arrivata al server. */
  saved: string
  cancel: string
  back: string

  sectionIdentity: string
  sectionSchedule: string
  sectionTarget: string
  sectionExecution: string
  sectionAlerts: string

  /**
   * Il job viene da un `cron.yaml` (R13). Va detto **prima**, non con un 409
   * dopo aver compilato: la riconciliazione riporterebbe indietro qualunque
   * modifica fatta da qui, e la modifica sparirebbe senza un errore.
   */
  managedTitle: string
  managedBody: string

  /** Riassunto in testa quando qualche campo non va. */
  invalidTitle: string
  /** Il nome è già di un altro job: 409 `job_name_taken`. */
  nameTaken: string
  /** Qualunque rifiuto senza un rimedio più preciso da suggerire. */
  unexpected: string
}

/** Etichette dei campi, e i requisiti scritti sotto. */
export interface JobFieldsContent {
  name: string
  /** Il nome è l'identità stabile del job: dirlo evita di rinominarlo per svista. */
  nameHint: string
  description: string
  optional: string

  mode: string
  modeCron: string
  modeInterval: string
  schedule: string
  scheduleHint: string
  every: string
  everyUnit: string
  timezone: string
  /** Il fuso è del job, non del browser (R1). */
  timezoneHint: string

  environments: string
  url: string
  method: string
  headers: string
  headerName: string
  headerValue: string
  addHeader: string
  removeHeader: string
  body: string

  timeout: string
  timeoutHint: string
  retries: string
  backoff: string
  overlap: string
  overlapHint: string
  alerts: string
  enabled: string
  enabledHint: string
}

/** Voci dei menu a tendina e delle caselle di scelta. */
export interface JobOptionsContent {
  /**
   * Politiche di attesa fra due tentativi (R5). I metodi HTTP non sono qui, e
   * non per dimenticanza: `POST` si chiama `POST` in tutte e cinque le lingue.
   */
  backoff: Record<JobBackoff, string>
  /**
   * Cosa fare quando un'occorrenza scatta mentre la precedente è in corso
   * (R41), con la conseguenza di ciascuna scelta scritta accanto: con la
   * risoluzione al secondo non è un caso raro, è la norma, e chi sceglie deve
   * sapere cosa sta scegliendo.
   */
  overlap: Record<JobOverlapPolicy, string>
  overlapHint: Record<JobOverlapPolicy, string>
  alerts: Record<JobAlertChannel, string>
  environments: Record<JobEnvironment, string>
  /** Unità dell'intervallo. */
  everyUnits: { s: string, m: string, h: string }
}

/**
 * Anteprima delle prossime esecuzioni.
 *
 * È la parte che trasforma un campo di testo in uno strumento, e ogni frase qui
 * esiste per togliere un equivoco preciso.
 */
export interface SchedulePreviewContent {
  title: string
  /** «Orari in {zone}» — il fuso va **dichiarato**, altrimenti confonde (R1). */
  inTimezone: string
  /**
   * La modalità a intervallo è ancorata all'epoch (SPEC §9): `every: 1h` scocca
   * all'ora piena UTC, non un'ora dopo il salvataggio. Senza questa riga
   * l'anteprima sembrerebbe sbagliata proprio a chi ha capito bene.
   */
  epochAnchored: string
  /** L'espressione è valida e non produce nessuna occorrenza (`0 0 30 2 *`). */
  never: string
  /** La schedulazione non si legge ancora: non c'è niente da mostrare. */
  invalid: string
  /** L'orario nominale non esiste in quel fuso: si parte al primo che esiste. */
  shifted: string
  /** L'orario accade due volte: si parte alla prima. */
  ambiguous: string
  /** Il valore autorevole, calcolato dallo scheduler. */
  scheduled: string
}

/**
 * Il piano in forza e i suoi tetti (R15), più i job che un cambio di piano ha
 * spento (R58).
 *
 * R15 chiede che i limiti siano applicati lato backend **e** che l'interfaccia
 * li dica: queste frasi sono la seconda metà, e i numeri dentro arrivano da
 * `GET /billing/subscription`.
 */
export interface PlanContent {
  title: string
  /** «{used} di {limit} cronjob». */
  jobsUsed: string
  /** «{used} cronjob attivi» — sui piani senza tetto rigido. */
  jobsUnlimited: string
  /** Perché «nuovo cronjob» è disattivato: si legge **prima** di compilare. */
  jobsFull: string
  /** «Frequenza minima: {value}». */
  minInterval: string
  /** «Log delle esecuzioni conservati {days} giorni». */
  retention: string

  suspendedTitle: string
  /** «{count} job fermi: riaccendine fino a {limit}». La scelta è dell'utente. */
  suspendedByJobLimit: string
  /** «{count} job più fitti di {value}: cambia la schedulazione». */
  suspendedByResolution: string
  /**
   * Perché un job sospeso per risoluzione non si riaccende: non è una questione
   * di posto, e riaccenderne un altro non ne libera. R58 pretende che
   * l'interfaccia lo dica, non che si limiti a rifiutare.
   */
  resolutionBlocked: string

  /** Rifiuti di piano che arrivano dal backend (403 `plan_limit_*`). */
  limitJobs: string
  limitResolution: string
  limitEnvironments: string
  upgrade: string
}

/** Come sta un job, in una parola. */
export interface JobStateContent {
  active: string
  paused: string
  /** Spento da un cambio di piano, per il tetto al numero (R58). */
  suspendedByJobLimit: string
  /** Spento da un cambio di piano, per la risoluzione (R58). */
  suspendedByResolution: string
  /** Non più presente nel `cron.yaml` da cui proveniva. */
  archived: string
  /** Definito in un `cron.yaml`: si modifica lì (R13). */
  managed: string
}

export interface DashboardContent {
  shell: ShellContent
  status: StatusContent
  home: HomeContent
  notFound: NotFoundContent
  auth: AuthContent
  jobs: JobsContent
}
