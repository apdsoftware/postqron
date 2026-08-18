import { isPublicPath, LOGIN_PATH, safeNextPath } from '~/utils/auth'

/**
 * La guardia di rotta (R14).
 *
 * ## Cos'è, e soprattutto cosa non è
 *
 * **Non è una difesa.** La dashboard è una SPA statica (SPEC §2): l'HTML servito
 * è un guscio uguale per tutti, `_redirects` lo consegna su qualunque percorso,
 * e non esiste alcun server che possa rifiutare una rotta. Chiunque può
 * disattivare questo file dagli strumenti di sviluppo e vedere il markup di
 * `/jobs`. Non ci troverebbe niente: i dati arrivano dal backend, che rifiuta le
 * richieste senza sessione valida, e quella è la difesa. Questa è la comodità
 * che evita a chi non è collegato di guardare una schermata di scheletri
 * destinata a diventare un errore.
 *
 * Scriverla sapendolo cambia due cose concrete. Non si nasconde niente «per
 * sicurezza», perché non c'è niente da nascondere che non sia già nel bundle. E
 * quando non si riesce a sapere se la sessione è valida (`unavailable`) si lascia
 * passare, invece di negare: negare non protegge nulla — il backend nega già — e
 * scaccerebbe dalla propria dashboard chi ha una sessione buona e la rete storta.
 *
 * ## Il primo caricamento
 *
 * Al primo caricamento la SPA non sa se la sessione è valida: deve chiederlo. Il
 * momento in cui non sa è gestito qui, ed è gestito **non mostrando niente**.
 * Questa funzione è `async`, e Nuxt attende la prima navigazione dentro
 * `app:created`, cioè prima di montare l'applicazione: finché `/auth/session`
 * non ha risposto, sullo schermo c'è il guscio vuoto — lo stesso che c'è mentre
 * il bundle si carica, con lo sfondo del tema già applicato. Non c'è nessun
 * istante in cui l'interfaccia mostra una risposta che non ha: né il modulo di
 * accesso a chi è collegato, né la dashboard a chi non lo è.
 *
 * L'alternativa — lasciar montare e correggere dopo — è quella che produce il
 * lampeggio, e nella direzione peggiore: la dashboard che compare per un istante
 * a chi non è collegato si legge come «c'ero dentro e mi ha buttato fuori».
 *
 * Il costo è un'andata e ritorno verso l'API prima del primo pixel utile.
 * È accettabile perché quella richiesta serve comunque — non c'è schermata che
 * possa fare a meno di sapere chi è l'utente — e ha una scadenza propria, perché
 * un backend appeso non lasci il guscio vuoto per sempre (vedi
 * `PROBE_TIMEOUT_MS` in `useSession()`).
 *
 * Alle navigazioni successive `ensure()` risponde già risolto e questa funzione
 * non attende niente.
 */
export default defineNuxtRouteMiddleware(async (to) => {
  const resolved = await useSession().ensure()

  if (isPublicPath(to.path)) {
    /*
     * Chi è già collegato non ha niente da fare sul modulo di accesso, e
     * lasciarcelo lo inviterebbe a riscrivere la password per l'unica ragione
     * che la casella è lì. Il `next` vale anche in questa direzione: è il caso
     * di chi apre `/login?next=/jobs/42` da un segnalibro con la sessione
     * ancora buona.
     */
    if (resolved === 'authenticated') {
      return navigateTo(safeNextPath(to.query.next) ?? '/', { replace: true })
    }
    return
  }

  if (resolved === 'anonymous') {
    /*
     * `replace: true`: la schermata protetta non ha mai avuto un contenuto, e
     * lasciarla nella cronologia farebbe sì che il «indietro» dopo l'accesso ci
     * riporti — cioè riporti alla guardia, che rimanda all'accesso.
     *
     * `next` porta l'indirizzo voluto fino a dopo l'accesso. Passa dalla
     * validazione come qualunque altro: qui viene da noi, ma la pagina di
     * accesso non ha modo di distinguerlo da quello scritto a mano nella barra
     * degli indirizzi, e la regola sta in un posto solo.
     */
    const next = safeNextPath(to.fullPath)
    return navigateTo(
      next === null ? LOGIN_PATH : { path: LOGIN_PATH, query: { next } },
      { replace: true },
    )
  }

  /*
   * `authenticated` passa. E passa anche `unavailable`: vedi sopra — la vista
   * mostrerà il proprio stato d'errore con «riprova» (R56), che è più onesto di
   * un rimbalzo verso un accesso che, con il backend irraggiungibile, non
   * funzionerebbe nemmeno.
   */
})
