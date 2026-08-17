import type { ComputedRef, Ref } from 'vue'
import type { DashboardContent } from '~/types/content'
import type { LocaleCode } from '~/utils/locale'
import { dashboardContent } from '~/content'
import { LOCALES, LOCALE_STORAGE_KEY, resolveLocale } from '~/utils/locale'

/**
 * Chiave dello stato condiviso. `useState` e non un `ref` a livello di modulo
 * perché lo stato deve appartenere all'istanza dell'applicazione: un `ref`
 * globale sopravvive fra i test e, con l'SSR riacceso per sbaglio, sarebbe
 * condiviso fra le richieste.
 */
const STATE_KEY = 'dashboard-locale'

export interface DashboardLocale {
  /** Lingua corrente dell'interfaccia. */
  locale: Ref<LocaleCode>
  /** Testi in quella lingua. */
  t: ComputedRef<DashboardContent>
  /** Valore dell'attributo `lang` del documento. */
  htmlLang: ComputedRef<string>
  /** Registra la scelta esplicita dell'utente e la applica (R32). */
  setLocale: (code: LocaleCode) => void
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
    // Nessun rimedio possibile: la lingua resta valida per questa sessione.
  }
}

/**
 * Lingua da usare all'avvio dell'applicazione.
 *
 * Viene calcolata una volta sola, all'inizializzazione dello stato. L'ordine di
 * precedenza sta tutto in `resolveLocale()`: qui si raccolgono le sorgenti.
 */
function initialLocale(): LocaleCode {
  return resolveLocale({
    /*
     * R33 (issue #445, backend): qui entrerà `profile.locale`, la lingua che
     * l'utente autenticato ha scelto nelle proprie impostazioni.
     *
     * Resta `null` finché l'endpoint del profilo non esiste, e `null` non è un
     * codice valido, quindi `resolveLocale()` scende al termine successivo:
     * oggi la precedenza è scelta locale, poi browser. Quando l'API ci sarà,
     * questa è l'unica riga da cambiare — il valore arriva dalla sessione già
     * caricata all'avvio, non da una fetch fatta qui dentro, perché
     * l'inizializzazione dello stato è sincrona.
     */
    profile: null,
    stored: rememberedLocale(),
    browser: browserLanguages(),
  })
}

/**
 * Lingua dell'interfaccia e testi corrispondenti.
 *
 * Diversamente dal sito pubblico la lingua non sta nella rotta ma nello stato
 * dell'applicazione: la dashboard è una SPA con `ssr: false`, le sue pagine non
 * vengono pre-renderizzate né indicizzate, e cambiare lingua non deve
 * comportare una navigazione. Il perché è argomentato in `utils/locale.ts`.
 */
export function useLocale(): DashboardLocale {
  const locale = useState<LocaleCode>(STATE_KEY, initialLocale)

  return {
    locale,
    t: computed(() => dashboardContent[locale.value]),
    htmlLang: computed(
      () => LOCALES.find(entry => entry.code === locale.value)?.htmlLang ?? locale.value,
    ),
    setLocale(code: LocaleCode) {
      locale.value = code
      /*
       * R32: la scelta esplicita prevale sul rilevamento e persiste fra le
       * visite. Il rilevamento automatico non scrive mai questa chiave: è la
       * differenza fra «il browser dice» e «l'utente ha scelto», ed è tutta la
       * ragione per cui la chiave esiste.
       */
      rememberLocale(code)
    },
  }
}
