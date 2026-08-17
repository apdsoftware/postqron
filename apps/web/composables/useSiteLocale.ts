import type { ComputedRef } from 'vue'
import type { SiteContent } from '~/types/content'
import type { LocaleCode } from '~/utils/locale'
import { siteContent } from '~/content'
import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY, isLocaleCode, localePath } from '~/utils/locale'

export interface SiteLocale {
  /** Lingua della pagina corrente, dedotta dal prefisso della rotta. */
  locale: ComputedRef<LocaleCode>
  /** Contenuti in quella lingua. */
  content: ComputedRef<SiteContent>
  /**
   * Antepone la lingua corrente a un percorso di `content/`.
   *
   * Ogni link interno deve passare di qui: il pre-rendering ha `failOnError` e
   * segue i link, quindi un `/#pricing` senza prefisso non è un dettaglio di
   * navigazione ma una build che si ferma.
   */
  href: (path: string) => string
}

/**
 * Lingua e contenuti della pagina corrente.
 *
 * La sorgente è il parametro `locale` della rotta — `pages/[locale]/` — e non
 * uno stato globale: su un sito statico l'indirizzo *è* la lingua, e due schede
 * aperte su lingue diverse devono restare due lingue diverse.
 */
export function useSiteLocale(): SiteLocale {
  const route = useRoute()

  const locale = computed<LocaleCode>(() => {
    const param = route.params.locale
    return isLocaleCode(param) ? param : DEFAULT_LOCALE
  })

  return {
    locale,
    content: computed(() => siteContent[locale.value]),
    href: (path: string) => localePath(path, locale.value),
  }
}

/**
 * Registra la scelta esplicita dell'utente (R32).
 *
 * Da qui in avanti la radice smisterà su questa lingua invece che su quella del
 * browser, anche nelle visite successive. Il rilevamento automatico non scrive
 * mai questa chiave: è la differenza fra «il browser dice» e «l'utente ha
 * scelto», ed è tutta la ragione per cui la chiave esiste.
 *
 * Una `localStorage` non disponibile — modalità privata di Safari, storage
 * pieno, cookie di terze parti bloccati in un iframe — non deve impedire il
 * cambio di lingua: la navigazione avviene comunque, si perde solo la memoria.
 */
export function rememberLocale(locale: LocaleCode): void {
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  }
  catch {
    // Nessun rimedio possibile e nessun danno: la lingua resta nell'indirizzo.
  }
}

/** Lingua scelta in una visita precedente, se ce n'è una valida. */
export function rememberedLocale(): LocaleCode | null {
  try {
    const stored = window.localStorage.getItem(LOCALE_STORAGE_KEY)
    return isLocaleCode(stored) ? stored : null
  }
  catch {
    return null
  }
}
