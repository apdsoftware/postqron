/**
 * Le rotte che l'autenticazione conosce, la regola per tornare dove si stava, e
 * l'unico numero che la dashboard ripete dal backend.
 *
 * Sta in `utils/` perché è logica pura: nessuna dipendenza da Vue, da Nuxt o dal
 * browser, quindi verificabile senza montare niente (`test/auth.test.ts`). È
 * anche il motivo per cui la guardia in `middleware/02.auth.global.ts` resta di
 * poche righe: quello che c'è da decidere è deciso qui.
 *
 * Il prefisso di lingua di §8-bis non è un caso in più da trattare: le due
 * funzioni che guardano un percorso lo tolgono prima di ragionarci (vedi
 * `stripLocale()`), così l'autenticazione continua a conoscere due rotte e non
 * dieci.
 */
import type { LocaleCode } from '~/utils/locale'
import { localePath, stripLocale } from '~/utils/locale'

/**
 * Lunghezza minima della password, copiata da `auth.MinPasswordLength` del
 * backend.
 *
 * **La regola non è questa: è quella del backend**, che la applica su
 * registrazione, reimpostazione e cambio password e che resta l'unico giudice.
 * Qui il numero esiste per una cosa sola — poterlo scrivere nel modulo *prima*
 * che l'utente scelga una password, invece di fargli scoprire il requisito con
 * un rifiuto dopo un viaggio verso il server. Una copia che diverge fa scrivere
 * un requisito sbagliato, non fa passare una password debole: il caso peggiore è
 * un 400 con il messaggio giusto, che il modulo mostra comunque.
 *
 * `test/content.test.ts` verifica che le cinque traduzioni del requisito citino
 * questo numero, così che cambiarlo qui senza cambiarle rompa la CI.
 */
export const MIN_PASSWORD_LENGTH = 12

/**
 * Dove si accede, **senza lingua**.
 *
 * Le rotte della dashboard sono prefissate (SPEC §8-bis): l'indirizzo vero è
 * `/it/login`. Qui e nel registro della navigazione i percorsi restano nella
 * forma neutra, perché la stessa schermata vale per tutte e cinque le lingue e
 * il prefisso si aggiunge al momento dell'uso con `localePath()` — o, nei
 * componenti, con l'`href()` di `useLocale()`.
 */
export const LOGIN_PATH = '/login'

/** Dove ci si registra, sempre senza lingua. Vedi [LOGIN_PATH]. */
export const REGISTER_PATH = '/register'

/**
 * Le sole schermate raggiungibili senza sessione.
 *
 * È un elenco chiuso e non un contrassegno su ogni pagina (`definePageMeta({
 * auth: false })`) per una ragione sola, ma decisiva: con l'elenco chiuso la
 * pagina **protetta** è quella che non si dichiara, e una schermata nuova nasce
 * protetta per dimenticanza. Col contrassegno sarebbe il contrario — la pagina
 * che dimentica la riga nasce pubblica — e la dimenticanza non si vedrebbe,
 * perché la pagina funziona.
 */
export const PUBLIC_PATHS: readonly string[] = [LOGIN_PATH, REGISTER_PATH]

/**
 * Dice se un percorso è raggiungibile senza sessione.
 *
 * Il prefisso di lingua viene tolto prima del confronto: `/it/login` e
 * `/de/login` sono la stessa schermata, e un elenco che li enumerasse tutti e
 * cinque per due rotte sarebbe dieci righe che si dimenticano a undici. La
 * forma senza prefisso continua a valere perché arriva ancora — da un vecchio
 * segnalibro, o da un `?next=` scritto a mano — e in entrambi i casi la
 * risposta giusta è la stessa.
 */
export function isPublicPath(path: string): boolean {
  return PUBLIC_PATHS.includes(normalizePath(stripLocale(path)))
}

/** Uno slash finale non cambia la schermata: `/login` e `/login/` sono la stessa. */
function normalizePath(path: string): string {
  const trimmed = path.replace(/\/+$/, '')
  return trimmed === '' ? '/' : trimmed
}

/**
 * Valida l'indirizzo di ritorno che viaggia nella query `next`.
 *
 * La dashboard ricade su `index.html` per qualunque percorso (`_redirects`):
 * chi apre un segnalibro su `/jobs/42` senza sessione deve finire all'accesso e
 * poi **su `/jobs/42`**, non sulla panoramica. Quel percorso attraversa la barra
 * degli indirizzi, quindi è un valore che arriva dall'esterno e va trattato
 * come tale.
 *
 * Non è una formalità: `?next=https://phishing.example/` produrrebbe un redirect
 * aperto che parte dal nostro dominio, subito dopo che l'utente ha scritto la
 * password. Un redirect aperto sulla pagina di accesso è la forma classica del
 * problema, perché è l'unico posto in cui l'utente ha appena dimostrato di
 * fidarsi. Le forme rifiutate sono quattro, e le prime due sono quelle che
 * ingannano l'occhio:
 *
 * - `//altro.example/x` — protocollo relativo: è **un'altra origin**, pur
 *   cominciando con uno slash come un percorso qualunque.
 * - `/\altro.example/x` — la stessa cosa scritta con una barra rovescia, che i
 *   browser normalizzano in `//`.
 * - qualunque cosa non cominci con `/` — indirizzo assoluto o percorso relativo.
 * - `/login` e `/register`, in qualunque lingua — non sono un pericolo, sono un
 *   anello: si tornerebbe all'accesso subito dopo esserne usciti.
 *
 * @returns il percorso se è utilizzabile, altrimenti `null`: chi chiama ricade
 *   sulla panoramica, che è sempre un posto sensato dove finire.
 */
export function safeNextPath(value: unknown): string | null {
  if (typeof value !== 'string' || value === '') return null
  if (!value.startsWith('/')) return null
  if (value.startsWith('//') || value.startsWith('/\\')) return null

  /*
   * Caratteri di controllo: un `\n` o un `\t` in mezzo all'indirizzo serve solo
   * a far leggere al browser qualcosa di diverso da ciò che si legge qui.
   */
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001F\u007F]/.test(value)) return null

  // Il confronto guarda il solo percorso: `?next=/login?x=1` è pur sempre l'anello.
  const path = value.split(/[?#]/)[0] ?? ''
  if (isPublicPath(path)) return null

  /*
   * La panoramica nuda è già il ripiego di chi chiama: dichiararla renderebbe
   * `?next=/` un parametro che non cambia niente, in ogni indirizzo di chiunque
   * apra la dashboard. La stessa rotta *con* una query invece si conserva: i
   * filtri di un elenco stanno lì, e tornare all'elenco senza i suoi filtri è
   * comunque aver perso il posto — ed è per questo che «nuda» significa
   * `value === path`, cioè che lo split di sopra non ha tolto niente.
   *
   * Con le rotte prefissate la panoramica si presenta come `/it`, non più solo
   * come `/`: si guarda quindi la forma senza lingua. Il ripiego di chi chiama
   * è la panoramica **nella lingua in cui si trova**, che con `?next=/de` non
   * si potrebbe comunque onorare senza portare l'utente in una terza lingua.
   */
  if (stripLocale(path).replace(/\/+$/, '') === '' && value === path) return null

  return value
}

/**
 * Porta un indirizzo di ritorno nella lingua in cui lo si sta per aprire.
 *
 * ## La domanda a cui risponde
 *
 * `?next=` viaggia nella barra degli indirizzi e porta il prefisso di quando è
 * stato scritto. Fra allora e adesso c'è il modulo di accesso, dove il
 * selettore è presente apposta (R32): chi arriva rimbalzato da `/en/jobs/42`,
 * non riconosce la lingua e passa all'italiano per capire cosa gli si chiede,
 * dopo aver scritto la password si vedrebbe restituire la schermata **in
 * inglese**. Sarebbe un cambio di lingua a metà di un'operazione, deciso da un
 * parametro che l'utente non ha scritto lui.
 *
 * Vince quindi la lingua della pagina su cui si è: è l'ultima cosa che l'utente
 * ha detto, ed è l'unica delle due che sia una scelta. Non contraddice la
 * precedenza dell'indirizzo argomentata in `utils/locale.ts` — la rispetta:
 * l'indirizzo che comanda è quello che si sta guardando, `/it/login`, e non un
 * pezzo di query composto da noi un rimbalzo fa.
 *
 * Il *dove* invece resta intatto: `next` dice quale schermata, e quella non si
 * tocca. Query e ancora viaggiano in coda senza essere interpretate — sono i
 * filtri e la posizione di ciò che si stava guardando.
 */
export function localizeNextPath(next: string, locale: LocaleCode): string {
  const index = next.search(/[?#]/)
  const path = index === -1 ? next : next.slice(0, index)
  const tail = index === -1 ? '' : next.slice(index)

  return localePath(path, locale) + tail
}

/**
 * Chiave del messaggio da mostrare in un modulo di autenticazione, in
 * `content.auth.errors`.
 */
export type AuthErrorKey
  = | 'credentials'
    | 'tooManyAttempts'
    | 'suspended'
    | 'invalidEmail'
    | 'weakPassword'
    | 'unexpected'

/**
 * Traduce il rifiuto del backend in ciò che si può dire a chi sta compilando.
 *
 * ## La cosa che questa funzione non fa, e non deve fare
 *
 * **Non distingue «utente inesistente» da «password sbagliata».** Non può: il
 * backend risponde `invalid_credentials` in entrambi i casi, e non per pigrizia.
 * Quando l'indirizzo non esiste verifica comunque una password finta
 * (`VerifyDecoy`) perché i due casi ci mettano lo stesso tempo — una difesa
 * contro l'enumerazione degli account che arriva a pareggiare i *tempi di
 * risposta*. Due messaggi diversi qui la annullerebbero con una frase: chi vuole
 * sapere se un indirizzo è registrato smetterebbe di cronometrare e leggerebbe.
 *
 * Vale anche per il 429: il limite conta i tentativi per indirizzo email
 * *esistente o no*, quindi «troppi tentativi» non dice a chi lo legge che
 * l'account c'è.
 *
 * Il 403 è l'eccezione, ed è lecita: il backend risponde «account sospeso» solo
 * **dopo** aver verificato che la password è giusta, quindi lo legge unicamente
 * chi era già in grado di entrare.
 *
 * ## Perché una funzione pura e non un `computed` nella pagina
 *
 * Perché è la regola più importante di queste due schermate, ed è l'unica che si
 * può verificare senza un browser: qui è un test da tre righe
 * (`test/auth.test.ts`), dentro un componente sarebbe un test di montaggio che
 * nessuno scrive.
 */
export function authErrorKey(error: { kind: string, status: number | null, code: string | null }): AuthErrorKey {
  // Prima del `kind`: un 429 ricade in `invalid` come tutti i 4xx restanti, e
  // «controlla i dati inseriti» a chi ha solo insistito troppo è fuorviante.
  if (error.status === 429) return 'tooManyAttempts'

  if (error.kind === 'unauthorized') return 'credentials'
  if (error.kind === 'forbidden') return 'suspended'

  if (error.code === 'invalid_email') return 'invalidEmail'
  if (error.code === 'weak_password') return 'weakPassword'

  /*
   * Tutto il resto — rete assente, 5xx, un 400 di cui non riconosciamo il
   * codice — diventa la stessa frase. È voluto: sono casi in cui l'unica cosa
   * vera da dire è «non è andata, riprova», e inventare distinzioni che non
   * portano a un'azione diversa aggiunge solo testo da tradurre.
   */
  return 'unexpected'
}
