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
 * ogni schermata, `home` è la schermata iniziale. Le sezioni della dashboard
 * vera — job, esecuzioni, chiavi API — aggiungeranno una chiave ciascuna con le
 * issue che le portano.
 */

/** Testi presenti su ogni schermata, indipendenti dalla rotta. */
export interface ShellContent {
  /**
   * Nome accessibile del selettore di lingua.
   *
   * Il selettore mostra solo i nomi delle cinque lingue: senza un'etichetta chi
   * usa uno screen reader sente un elenco di parole senza sapere cosa scelgono.
   */
  languageLabel: string
}

/** Schermata iniziale. Resta lo scaffold finché non arriva il layout Flowbite. */
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

export interface DashboardContent {
  shell: ShellContent
  home: HomeContent
}
