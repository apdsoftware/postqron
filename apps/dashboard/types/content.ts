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

export interface DashboardContent {
  shell: ShellContent
  status: StatusContent
  home: HomeContent
  notFound: NotFoundContent
  auth: AuthContent
}
