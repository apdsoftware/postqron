/**
 * Icone dell'interfaccia.
 *
 * Sono i tracciati SVG del template `themesberg/flowbite-admin-dashboard`,
 * copiati verbatim: stesso disegno, stessa griglia `0 0 20 20`, stesso
 * `fill-rule`. Il template li scrive in linea in ogni pagina; qui stanno in un
 * registro perché la stessa icona compare in più componenti e un tracciato
 * duplicato è un tracciato che prima o poi diverge.
 *
 * **Perché un registro e non un pacchetto di icone.** Un pacchetto porta con sé
 * centinaia di icone e un componente che le risolve a runtime; qui ne servono
 * dieci, e ognuna è una stringa costante che il bundler mette nel chunk in cui
 * viene usata. Il costo in JavaScript di questa scelta è la somma dei tracciati
 * che si usano davvero — che è il punto di R53-bis.
 *
 * Le icone sono decorative: chi le disegna deve marcarle `aria-hidden` e dare il
 * nome accessibile al controllo che le contiene. Lo fa `AppIcon.vue`.
 */

/**
 * Un'icona: uno o più tracciati e la regola di riempimento con cui vanno resi.
 *
 * `fillRule` non è un dettaglio estetico — sui tracciati che si autointersecano
 * (l'ingranaggio, il cerchio con il punto esclamativo) `evenodd` è ciò che
 * bucherebbe il centro, e senza il disegno diventa una macchia piena.
 */
export interface IconDefinition {
  paths: readonly string[]
  fillRule?: 'evenodd'
}

export const ICONS = {
  /** Panoramica: il grafico a torta della voce «Dashboard» del template. */
  overview: {
    paths: [
      'M2 10a8 8 0 018-8v8h8a8 8 0 11-16 0z',
      'M12 2.252A8.014 8.014 0 0117.748 8H12V2.252z',
    ],
  },

  /** Tre righe: apre il cassetto della barra laterale sotto i 1024 px. */
  menu: {
    paths: [
      'M3 5a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1zM3 10a1 1 0 011-1h6a1 1 0 110 2H4a1 1 0 01-1-1zM3 15a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1z',
    ],
    fillRule: 'evenodd',
  },

  /** Croce: chiude il cassetto. */
  close: {
    paths: [
      'M4.293 4.293a1 1 0 011.414 0L10 8.586l4.293-4.293a1 1 0 111.414 1.414L11.414 10l4.293 4.293a1 1 0 01-1.414 1.414L10 11.414l-4.293 4.293a1 1 0 01-1.414-1.414L8.586 10 4.293 5.707a1 1 0 010-1.414z',
    ],
    fillRule: 'evenodd',
  },

  /** Luna: passa al tema scuro. */
  moon: {
    paths: ['M17.293 13.293A8 8 0 016.707 2.707a8.001 8.001 0 1010.586 10.586z'],
  },

  /** Sole: torna al tema chiaro. */
  sun: {
    paths: [
      'M10 2a1 1 0 011 1v1a1 1 0 11-2 0V3a1 1 0 011-1zm4 8a4 4 0 11-8 0 4 4 0 018 0zm-.464 4.95l.707.707a1 1 0 001.414-1.414l-.707-.707a1 1 0 00-1.414 1.414zm2.12-10.607a1 1 0 010 1.414l-.706.707a1 1 0 11-1.414-1.414l.707-.707a1 1 0 011.414 0zM17 11a1 1 0 100-2h-1a1 1 0 100 2h1zm-7 4a1 1 0 011 1v1a1 1 0 11-2 0v-1a1 1 0 011-1zM5.05 6.464A1 1 0 106.465 5.05l-.708-.707a1 1 0 00-1.414 1.414l.707.707zm1.414 8.486l-.707.707a1 1 0 01-1.414-1.414l.707-.707a1 1 0 011.414 1.414zM4 11a1 1 0 100-2H3a1 1 0 000 2h1z',
    ],
    fillRule: 'evenodd',
  },

  /** Ingranaggio: la lingua vive nel piede della barra laterale, accanto a questa. */
  settings: {
    paths: [
      'M11.49 3.17c-.38-1.56-2.6-1.56-2.98 0a1.532 1.532 0 01-2.286.948c-1.372-.836-2.942.734-2.106 2.106.54.886.061 2.042-.947 2.287-1.561.379-1.561 2.6 0 2.978a1.532 1.532 0 01.947 2.287c-.836 1.372.734 2.942 2.106 2.106a1.532 1.532 0 012.287.947c.379 1.561 2.6 1.561 2.978 0a1.533 1.533 0 012.287-.947c1.372.836 2.942-.734 2.106-2.106a1.533 1.533 0 01.947-2.287c1.561-.379 1.561-2.6 0-2.978a1.532 1.532 0 01-.947-2.287c.836-1.372-.734-2.942-2.106-2.106a1.532 1.532 0 01-2.287-.947zM10 13a3 3 0 100-6 3 3 0 000 6z',
    ],
    fillRule: 'evenodd',
  },

  /** Cerchio con punto esclamativo: accompagna lo stato di errore. */
  alert: {
    paths: [
      'M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z',
    ],
    fillRule: 'evenodd',
  },

  /** Spunta: accompagna un esito riuscito. */
  check: {
    paths: [
      'M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z',
    ],
    fillRule: 'evenodd',
  },

  /** Freccia a sinistra: il «torna indietro» della pagina 404 del template. */
  back: {
    paths: [
      'M12.707 5.293a1 1 0 010 1.414L9.414 10l3.293 3.293a1 1 0 01-1.414 1.414l-4-4a1 1 0 010-1.414l4-4a1 1 0 011.414 0z',
    ],
    fillRule: 'evenodd',
  },

  /** Orologio: i cronjob, e la prossima esecuzione. */
  clock: {
    paths: [
      'M10 18a8 8 0 100-16 8 8 0 000 16zm1-12a1 1 0 10-2 0v4a1 1 0 00.293.707l2.828 2.829a1 1 0 101.415-1.415L11 9.586V6z',
    ],
    fillRule: 'evenodd',
  },

  /** Più: aggiunge una riga — un cronjob, un header. */
  plus: {
    paths: [
      'M10 5a1 1 0 011 1v3h3a1 1 0 110 2h-3v3a1 1 0 11-2 0v-3H6a1 1 0 110-2h3V6a1 1 0 011-1z',
    ],
    fillRule: 'evenodd',
  },

  /** Cestino: elimina. */
  trash: {
    paths: [
      'M9 2a1 1 0 00-.894.553L7.382 4H4a1 1 0 000 2v10a2 2 0 002 2h8a2 2 0 002-2V6a1 1 0 100-2h-3.382l-.724-1.447A1 1 0 0011 2H9zM7 8a1 1 0 012 0v6a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v6a1 1 0 102 0V8a1 1 0 00-1-1z',
    ],
    fillRule: 'evenodd',
  },

  /** Freccia di riproduzione: esegue adesso (trigger manuale). */
  play: {
    paths: [
      'M10 18a8 8 0 100-16 8 8 0 000 16zM9.555 7.168A1 1 0 008 8v4a1 1 0 001.555.832l3-2a1 1 0 000-1.664l-3-2z',
    ],
    fillRule: 'evenodd',
  },

  /** Due barre: mette in pausa. */
  pause: {
    paths: [
      'M18 10a8 8 0 11-16 0 8 8 0 0116 0zM7 8a1 1 0 012 0v4a1 1 0 11-2 0V8zm5-1a1 1 0 00-1 1v4a1 1 0 102 0V8a1 1 0 00-1-1z',
    ],
    fillRule: 'evenodd',
  },

  /** Matita: modifica. */
  edit: {
    paths: [
      'M13.586 3.586a2 2 0 112.828 2.828l-.793.793-2.828-2.828.793-.793zM11.379 5.793L3 14.172V17h2.828l8.38-8.379-2.83-2.828z',
    ],
    fillRule: 'evenodd',
  },

  /** Chevron verso il basso: apre un menu a tendina. */
  chevronDown: {
    paths: [
      'M5.293 7.293a1 1 0 011.414 0L10 10.586l3.293-3.293a1 1 0 111.414 1.414l-4 4a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414z',
    ],
    fillRule: 'evenodd',
  },
} as const satisfies Record<string, IconDefinition>

export type IconName = keyof typeof ICONS
