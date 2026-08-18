import type { ComputedRef } from 'vue'
import type { DashboardContent } from '~/types/content'
import type { LocaleCode } from '~/utils/locale'
import { dashboardContent } from '~/content'
import {
  LOCALES,
  LOCALE_STORAGE_KEY,
  localeFromPath,
  localePath,
  resolveLocale,
} from '~/utils/locale'

/**
 * Chiave dello stato condiviso. `useState` e non un `ref` a livello di modulo
 * perché lo stato deve appartenere all'istanza dell'applicazione: un `ref`
 * globale sopravvive fra i test e, con l'SSR riacceso per sbaglio, sarebbe
 * condiviso fra le richieste.
 */
const STATE_KEY = 'dashboard-locale-preference'

export interface DashboardLocale {
  /**
   * Lingua dell'interfaccia: quella dichiarata dall'indirizzo.
   *
   * È in sola lettura, e la ragione non è di stile. La lingua *è* l'indirizzo
   * (SPEC §8-bis): scriverci sopra senza navigare produrrebbe una pagina che si
   * legge in francese e si trova su `/it/…`, cioè un indirizzo che mente a chi
   * lo copia. Si cambia con [DashboardLocale.setLocale], che naviga.
   */
  locale: ComputedRef<LocaleCode>
  /**
   * Lingua in cui aprire un indirizzo che non ne dichiara una.
   *
   * La usa lo smistamento (`middleware/01.locale.global.ts`) e nient'altro:
   * profilo, scelta memorizzata, browser, inglese, nell'ordine di
   * `resolveLocale()`.
   */
  preferred: ComputedRef<LocaleCode>
  /** Testi nella lingua corrente. */
  t: ComputedRef<DashboardContent>
  /** Valore dell'attributo `lang` del documento. */
  htmlLang: ComputedRef<string>
  /** Cambia lingua: registra la scelta (R32) e porta la stessa pagina lì. */
  setLocale: (code: LocaleCode) => void
  /**
   * Antepone la lingua corrente a un percorso scritto nel codice.
   *
   * Ogni link interno deve passare di qui. Un `to="/jobs"` scritto a mano non
   * darebbe 404 — la SPA ricade su `index.html` e il middleware lo smisterebbe
   * — ma costerebbe un reindirizzamento a ogni click e, sull'indirizzo di
   * partenza, riporterebbe l'utente alla lingua preferita invece che a quella
   * della pagina che sta guardando.
   */
  href: (path: string) => string
}

/**
 * Lista delle lingue preferite del browser.
 *
 * `navigator.languages` è l'elenco completo in ordine di preferenza;
 * `navigator.language` è il ripiego per i browser che non lo espongono. Il
 * controllo su `navigator` non è difensivo per abitudine: questo modulo viene
 * importato anche dai test, che girano in Node.
 */
function browserLanguages(): readonly string[] {
  if (typeof navigator === 'undefined') return []

  return navigator.languages ?? (navigator.language ? [navigator.language] : [])
}

/**
 * Scelta esplicita fatta in una visita precedente, se ce n'è una valida.
 *
 * Una `localStorage` non disponibile — modalità privata di Safari, storage
 * pieno, cookie di terze parti bloccati in un iframe — non deve impedire alla
 * dashboard di aprirsi: si perde solo la memoria della scelta.
 */
function rememberedLocale(): string | null {
  try {
    return window.localStorage.getItem(LOCALE_STORAGE_KEY)
  }
  catch {
    return null
  }
}

function rememberLocale(locale: LocaleCode): void {
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  }
  catch {
    // Nessun rimedio possibile: la lingua resta valida per questa sessione, e
    // soprattutto resta nell'indirizzo, che è dove conta davvero.
  }
}

/**
 * Lingua in cui smistare, calcolata una volta sola all'inizializzazione dello
 * stato. L'ordine di precedenza sta tutto in `resolveLocale()`: qui si
 * raccolgono le sorgenti.
 */
function initialPreference(): LocaleCode {
  return resolveLocale({
    /*
     * R33: qui entrerà `language` del profilo, che oggi `UserResponse` non
     * espone ancora (`services/api/internal/httpapi/auth.go`). È il valore con
     * cui il backend compone i link delle email, quindi farci smistare la
     * radice è ciò che rende indistinguibili i due modi di entrare.
     *
     * `null` non è un codice valido, quindi `resolveLocale()` scende al termine
     * successivo: oggi la precedenza è scelta locale, poi browser.
     */
    profile: null,
    stored: rememberedLocale(),
    browser: browserLanguages(),
  })
}

/**
 * Lingua dell'interfaccia e testi corrispondenti.
 *
 * La sorgente è il **percorso**, come su `apps/web` e per le stesse ragioni,
 * meno una: qui non c'è pre-rendering, ma restano la condivisibilità
 * dell'indirizzo e i link composti da fuori — a partire da quelli delle email
 * (R21). L'argomento per esteso, e la precedenza fra indirizzo e profilo, stanno
 * in `utils/locale.ts`.
 *
 * La conseguenza pratica da tenere a mente è che due schede della stessa
 * dashboard possono ora stare su lingue diverse, perché sono due indirizzi
 * diversi. Prima non potevano, e la differenza si vede qui: non c'è più uno
 * stato «lingua corrente» da tenere allineato, c'è un percorso da leggere.
 */
export function useLocale(): DashboardLocale {
  const router = useRouter()

  /*
   * `router.currentRoute` e non `useRoute()`: questo composable viene chiamato
   * anche da un middleware, dove la rotta corrente è ancora quella di partenza
   * e `useRoute()` avvisa di non essere nel posto giusto. Il ref è lo stesso, e
   * la reattività nei componenti non cambia.
   */
  const preference = useState<LocaleCode>(STATE_KEY, initialPreference)
  const locale = computed<LocaleCode>(
    () => localeFromPath(router.currentRoute.value.path) ?? preference.value,
  )

  return {
    locale,
    preferred: computed(() => preference.value),
    t: computed(() => dashboardContent[locale.value]),
    htmlLang: computed(
      () => LOCALES.find(entry => entry.code === locale.value)?.htmlLang ?? locale.value,
    ),
    href: (path: string) => localePath(path, locale.value),

    setLocale(code: LocaleCode) {
      /*
       * R32: la scelta esplicita prevale sul rilevamento e persiste fra le
       * visite. Il rilevamento automatico non scrive mai questa chiave: è la
       * differenza fra «il browser dice» e «l'utente ha scelto», ed è tutta la
       * ragione per cui la chiave esiste.
       *
       * Si aggiorna anche la preferenza in memoria, e non solo `localStorage`:
       * viene letto una volta all'avvio, e senza questa riga chi cambia lingua e
       * poi apre la radice nella stessa visita verrebbe smistato sulla lingua
       * di prima.
       */
      rememberLocale(code)
      preference.value = code

      /*
       * E si naviga, perché la lingua è l'indirizzo. `replace` e non `push`:
       * cambiare lingua non è andare da un'altra parte, è guardare la stessa
       * cosa scritta in un'altra lingua. Con `push`, «indietro» riporterebbe
       * alla lingua precedente invece che alla pagina precedente, e chi ne
       * prova tre per trovare la propria dovrebbe premerlo tre volte per
       * uscire dalla schermata.
       *
       * `query` e `hash` si conservano: sono i filtri e la posizione di ciò che
       * si sta guardando, e perderli tradurrebbe la pagina buttandone via il
       * contenuto.
       */
      const current = router.currentRoute.value
      void router.replace({
        // `localePath()` sostituisce il prefisso che c'è già invece di
        // aggiungerne un secondo: `/it/jobs/42` diventa `/fr/jobs/42`.
        path: localePath(current.path, code),
        query: current.query,
        hash: current.hash,
      })
    },
  }
}
