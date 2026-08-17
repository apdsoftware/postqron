import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in italiano, tradotti da `content/en.ts`. */
export const it: DashboardContent = {
  shell: {
    languageLabel: 'Lingua',
    skipToContent: 'Vai al contenuto',
    navigationLabel: 'Navigazione principale',
    openNavigation: 'Apri la navigazione',
    closeNavigation: 'Chiudi la navigazione',
    nav: {
      overview: 'Panoramica',
    },
    toLightTheme: 'Passa al tema chiaro',
    toDarkTheme: 'Passa al tema scuro',
  },

  home: {
    title: 'Panoramica',
    intro: 'Il servizio Postqron che esegue i tuoi cronjob, e se sta rispondendo.',
    backendTitle: 'Stato del servizio',
    apiBaseLabel: 'Indirizzo base dell\'API',
    check: 'Verifica lo stato del backend',
    checking: 'Verifica in corso…',
    unreachable: 'Backend non raggiungibile',
  },

  notFound: {
    title: 'Pagina non trovata',
    intro: 'Questo indirizzo non corrisponde a nessuna schermata della dashboard. Potrebbe essere cambiato, o il collegamento potrebbe essere sbagliato.',
    back: 'Torna alla panoramica',
  },
}
