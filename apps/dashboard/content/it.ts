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

  status: {
    loading: 'Caricamento…',
    errorTitle: 'Qualcosa non ha funzionato',
    retry: 'Riprova',
    errors: {
      network: 'Il backend non ha risposto. Controlla la connessione e riprova.',
      unauthorized: 'La sessione è scaduta. Accedi di nuovo per continuare.',
      forbidden: 'Non hai accesso a questa risorsa.',
      notFound: 'Questa risorsa non c\'è più.',
      invalid: 'La richiesta è stata rifiutata. Controlla i dati inseriti.',
      server: 'Il backend ha avuto un problema. Riprova fra un momento.',
    },
  },

  home: {
    title: 'Panoramica',
    intro: 'Il servizio Postqron che esegue i tuoi cronjob, e se sta rispondendo.',
    backendTitle: 'Stato del servizio',
    apiBaseLabel: 'Indirizzo base dell\'API',
    statusLabel: 'Stato',
    environmentLabel: 'Ambiente',
    versionLabel: 'Versione',
    check: 'Controlla di nuovo',
  },

  notFound: {
    title: 'Pagina non trovata',
    intro: 'Questo indirizzo non corrisponde a nessuna schermata della dashboard. Potrebbe essere cambiato, o il collegamento potrebbe essere sbagliato.',
    back: 'Torna alla panoramica',
  },
}
