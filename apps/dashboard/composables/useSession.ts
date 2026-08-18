import type { Ref } from 'vue'
import { ApiError } from '~/utils/api'
import { LOGIN_PATH, safeNextPath } from '~/utils/auth'
import { DEFAULT_LOCALE, localeFromPath, localePath } from '~/utils/locale'

/**
 * La sessione dell'utente, per quel poco che una SPA statica può saperne (R14).
 *
 * ## Dove vive la sessione
 *
 * **Non qui.** Vive in un cookie `pq_session` marcato `HttpOnly`, che scrive il
 * backend e che JavaScript non può leggere (`internal/httpapi/identity.go`). La
 * dashboard non ha nessuna credenziale in mano: né in `localStorage`, né in
 * `sessionStorage`, né in una variabile. Il ragionamento per esteso — e perché
 * il seme della issue #24 prevedeva invece un token in una testata — sta in
 * `composables/useApi.ts`.
 *
 * Quello che c'è qui dentro è un'altra cosa: **la memoria dell'ultima risposta
 * di `/auth/session`**. È un dato derivato, non una credenziale, e le due
 * proprietà che ne discendono vanno tenute a mente leggendo tutto il resto:
 *
 * 1. Sta in memoria e basta. Un ricaricamento la perde e la ricostruisce
 *    chiedendo di nuovo; non si conserva da nessuna parte, perché conservarla
 *    significherebbe fidarsi di una copia che il backend può aver invalidato un
 *    secondo dopo.
 * 2. Può essere **sbagliata**, e non è un difetto: fra la risposta e il momento
 *    in cui la si legge, la sessione può essere scaduta o essere stata revocata
 *    da un altro dispositivo. Per questo la guardia che la usa è una comodità e
 *    non una difesa — la difesa è il backend, che rifiuta le richieste senza
 *    sessione valida, e la reazione a quel rifiuto è [DashboardSession.expire].
 *
 * ## Perché non è un `useApiResource()`
 *
 * `useApiResource()` fa partire la richiesta al montaggio di un componente e ne
 * restituisce gli stati a quel componente. La sessione serve **prima**: la
 * decide la guardia di rotta, che gira quando nessun componente è ancora
 * montato, e vale per tutta l'applicazione e non per una vista. Sono i quattro
 * stati di [SessionStatus] a fare qui il lavoro che là fanno `pending`, `error` e
 * `data` — e le viste continuano a usare `useApiResource()` per i propri dati,
 * compreso il caso in cui uno di quei dati torni 401.
 */

/**
 * Cosa sappiamo, adesso, della sessione.
 *
 * Sono quattro e non due perché «non collegato» e «non lo so» portano a due
 * comportamenti diversi, e confonderli è il modo in cui una guardia lato client
 * diventa una seccatura:
 *
 * - `unknown` — non l'abbiamo ancora chiesto. È lo stato di partenza a ogni
 *   caricamento della pagina, ed è l'unico che nessuna schermata deve mai
 *   vedere: la guardia lo risolve prima che l'applicazione si monti.
 * - `authenticated` — l'ultima risposta di `/auth/session` era un utente.
 * - `anonymous` — l'ultima risposta era 401. È una risposta, non un guasto: è
 *   *la* risposta alla domanda che stavamo facendo.
 * - `unavailable` — non si è potuto chiedere: backend spento, rete assente,
 *   500. Non è «non collegato», e trattarlo come tale manderebbe all'accesso
 *   chi ha una sessione validissima e un attimo di rete storta — dove peraltro
 *   nemmeno il login funzionerebbe. Vedi la guardia per cosa se ne fa.
 */
export type SessionStatus = 'unknown' | 'authenticated' | 'anonymous' | 'unavailable'

/**
 * L'utente collegato, nella forma in cui il backend lo serializza
 * (`UserResponse` in `internal/httpapi/auth.go`).
 *
 * I nomi restano quelli del JSON, `snake_case` compreso: rinominarli qui
 * significherebbe mantenere una tabella di corrispondenza che si rompe in
 * silenzio quando il backend aggiunge un campo.
 */
export interface SessionUser {
  id: string
  email: string
  full_name: string
  role: string
  timezone: string
  email_verified: boolean
}

/** Involucro di `/auth/session` e di `/auth/login` (`SessionEnvelope`). */
interface SessionEnvelope {
  user: SessionUser
}

export interface DashboardSession {
  status: Ref<SessionStatus>
  /** L'utente collegato, `null` quando non ce n'è uno. */
  user: Ref<SessionUser | null>
  /**
   * Vero quando la sessione è finita **da sé** — scadenza, o revoca da un altro
   * dispositivo — e non perché l'utente è uscito.
   *
   * Esiste per una riga sola sulla pagina di accesso, e quella riga è tutto il
   * punto: chi si vede comparire il modulo di accesso in mezzo a un lavoro
   * merita di sapere *perché*, altrimenti l'unica lettura disponibile è che
   * l'applicazione si è rotta e gli ha fatto perdere quello che stava facendo.
   */
  interrupted: Ref<boolean>
  /**
   * Risolve lo stato, chiedendolo al backend se serve.
   *
   * Chiede una volta sola per caricamento della pagina, tranne quando la
   * risposta precedente è stata `unavailable`: quella non è un esito, è
   * un'assenza di esito, e va ritentata alla navigazione successiva.
   */
  ensure: () => Promise<SessionStatus>
  /** Apre una sessione. Rilancia l'`ApiError` perché il modulo lo mostri. */
  signIn: (email: string, password: string) => Promise<void>
  /** Registra un account. Vedi `pages/register.vue` per cosa se ne può dire. */
  register: (input: RegisterInput) => Promise<void>
  /** Chiude la sessione, qui e sul backend. */
  signOut: () => Promise<void>
  /**
   * La sessione non vale più, e ce ne siamo accorti da un 401 arrivato a metà
   * lavoro. La chiama `useApi()`, e nessun altro.
   */
  expire: () => void
}

export interface RegisterInput {
  email: string
  password: string
  fullName: string
}

/*
 * Chiavi dello stato condiviso. `useState` e non dei `ref` a livello di modulo
 * per la stessa ragione di `useLocale()`: lo stato appartiene all'istanza
 * dell'applicazione, e un `ref` globale sopravviverebbe fra i test.
 */
const STATUS_KEY = 'dashboard-session-status'
const USER_KEY = 'dashboard-session-user'
const INTERRUPTED_KEY = 'dashboard-session-interrupted'
const PROBE_KEY = 'dashboard-session-probe'

/**
 * Quanto si aspetta la risposta di `/auth/session` prima di rinunciare.
 *
 * Serve perché quella richiesta è l'unica che blocca l'avvio: finché non
 * risponde, l'applicazione non si monta. `fetch` non ha una scadenza propria, e
 * un backend che accetta la connessione e non risponde più — non spento:
 * appeso — lascerebbe la dashboard su una pagina bianca per sempre, che è il
 * guasto peggiore di tutti perché non dice niente. Scaduto il tempo si riparte
 * come `unavailable`: le viste mostreranno il proprio stato d'errore, con
 * «riprova», invece del nulla.
 *
 * `AbortSignal.timeout()` annulla con un `TimeoutError` e non con un
 * `AbortError`, quindi `useApi()` non lo scambia per un cambio di pagina e lo
 * classifica come guasto di rete: è ciò che vogliamo.
 */
const PROBE_TIMEOUT_MS = 8_000

export function useSession(): DashboardSession {
  const status = useState<SessionStatus>(STATUS_KEY, () => 'unknown')
  const user = useState<SessionUser | null>(USER_KEY, () => null)
  const interrupted = useState<boolean>(INTERRUPTED_KEY, () => false)

  /*
   * La richiesta in volo, per unire le chiamate concorrenti in una sola. Serve
   * davvero: la guardia gira su ogni navigazione, e due navigazioni ravvicinate
   * al primo caricamento — un redirect è una navigazione — chiederebbero due
   * volte la stessa cosa. Sta in `useState` e non in una variabile di modulo
   * perché la sua durata è quella dell'applicazione, non quella del processo.
   */
  const probe = useState<Promise<SessionStatus> | null>(PROBE_KEY, () => null)

  const router = useRouter()

  /**
   * L'accesso nella lingua della pagina da cui si esce.
   *
   * Le rotte sono prefissate (SPEC §8-bis) e questo composable porta l'utente
   * all'accesso da due strade — l'uscita volontaria e la sessione scaduta — che
   * partono entrambe da una schermata già in una lingua. Mandarlo su un
   * `/login` senza prefisso lo farebbe smistare sulla lingua *preferita*, che
   * dopo una scelta col selettore in questa visita è un'altra: si uscirebbe
   * dall'italiano e si arriverebbe all'accesso in inglese, senza aver toccato
   * niente.
   */
  function loginPath(): string {
    const path = router.currentRoute.value.path
    return localePath(LOGIN_PATH, localeFromPath(path) ?? DEFAULT_LOCALE)
  }

  function adopt(envelope: SessionEnvelope): void {
    user.value = envelope.user
    status.value = 'authenticated'
    interrupted.value = false
  }

  function forget(status_: Extract<SessionStatus, 'anonymous' | 'unavailable'>): void {
    user.value = null
    status.value = status_
  }

  async function probeSession(): Promise<SessionStatus> {
    /*
     * `onUnauthorized: 'throw'` è obbligatorio qui: un 401 su questa chiamata è
     * la risposta che stiamo cercando, non una sessione da chiudere. Senza,
     * `useApi()` chiamerebbe `expire()`, che porta all'accesso — e ci porterebbe
     * chiunque apra la dashboard da scollegato, con l'avviso «la sessione è
     * terminata» addosso a chi non ne ha mai avuta una.
     */
    try {
      const envelope = await useApi().request<SessionEnvelope>('/auth/session', {
        signal: AbortSignal.timeout(PROBE_TIMEOUT_MS),
        onUnauthorized: 'throw',
      })
      adopt(envelope)
    }
    catch (cause) {
      if (!(cause instanceof ApiError)) throw cause

      forget(cause.kind === 'unauthorized' ? 'anonymous' : 'unavailable')
    }

    return status.value
  }

  async function ensure(): Promise<SessionStatus> {
    if (status.value === 'authenticated' || status.value === 'anonymous') return status.value
    if (probe.value) return await probe.value

    probe.value = probeSession().finally(() => {
      probe.value = null
    })
    return await probe.value
  }

  async function signIn(email: string, password: string): Promise<void> {
    const envelope = await useApi().request<SessionEnvelope>('/auth/login', {
      method: 'POST',
      body: { email, password },
      onUnauthorized: 'throw',
    })
    adopt(envelope)
  }

  async function register(input: RegisterInput): Promise<void> {
    await useApi().request<unknown>('/auth/register', {
      method: 'POST',
      body: { email: input.email, password: input.password, full_name: input.fullName },
      onUnauthorized: 'throw',
    })
  }

  async function signOut(): Promise<void> {
    /*
     * L'ordine è: prima il backend, poi lo stato locale, e comunque lo stato
     * locale. Il backend è ciò che revoca davvero la sessione e cancella il
     * cookie; se non risponde, restare «collegati» in interfaccia sarebbe la
     * cosa meno utile possibile a chi ha appena premuto «esci» — magari su un
     * computer che non è suo. Si esce lo stesso, e la sessione lato server
     * scadrà da sé.
     *
     * `/auth/logout` risponde 204 anche senza sessione, quindi il caso normale
     * non ha rami: l'unico errore possibile è che il backend non risponda.
     */
    try {
      await useApi().request<unknown>('/auth/logout', { method: 'POST', onUnauthorized: 'throw' })
    }
    finally {
      const destination = loginPath()
      forget('anonymous')
      interrupted.value = false
      await router.push(destination)
    }
  }

  function expire(): void {
    // Già chiuso: due richieste in volo che tornano 401 insieme sono la norma,
    // e la seconda non deve sovrascrivere il `next` che ha calcolato la prima.
    if (status.value === 'anonymous') return

    forget('anonymous')
    interrupted.value = true

    /*
     * Dove si era. `fullPath` e non `path`: i filtri e la pagina di un elenco
     * stanno nella query, e tornare all'elenco senza i suoi filtri è comunque
     * aver perso il posto. Quello che non si può salvare è ciò che l'utente
     * aveva scritto in un modulo e non ha ancora inviato: la navigazione lo
     * smonta. È il motivo per cui `interrupted` esiste — non potendo conservare
     * il lavoro, gli si dice almeno cosa gliel'ha portato via.
     */
    const from = router.currentRoute.value.fullPath
    const next = safeNextPath(from)
    const destination = loginPath()

    void router.push(next === null ? destination : { path: destination, query: { next } })
  }

  return { status, user, interrupted, ensure, signIn, register, signOut, expire }
}
