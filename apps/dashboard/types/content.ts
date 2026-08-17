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
 * ogni schermata, `home` e `notFound` sono due schermate. Le sezioni della
 * dashboard vera — job, esecuzioni, chiavi API — aggiungeranno una chiave
 * ciascuna con le issue che le portano, più una voce in `shell.nav`.
 */
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
}

/** Schermata iniziale. */
export interface HomeContent {
  title: string
  intro: string
  backendTitle: string
  /** Etichetta dell'indirizzo dell'API; il valore accanto non è tradotto. */
  apiBaseLabel: string
  /** Pulsante di verifica, a riposo e mentre la richiesta è in corso. */
  check: string
  checking: string
  /** Messaggio mostrato quando il backend non risponde. */
  unreachable: string
}

/** Indirizzo che non corrisponde a nessuna schermata. */
export interface NotFoundContent {
  title: string
  intro: string
  /** Collegamento di rientro alla panoramica. */
  back: string
}

export interface DashboardContent {
  shell: ShellContent
  home: HomeContent
  notFound: NotFoundContent
}
