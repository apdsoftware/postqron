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
    account: {
      open: 'Menu dell\'account',
      signedInAs: 'Collegato come',
      signOut: 'Esci',
    },
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

  auth: {
    signIn: {
      title: 'Accedi',
      submit: 'Accedi',
      submitting: 'Accesso in corso…',
      noAccount: 'Non hai ancora un account?',
      noAccountLink: 'Creane uno',
      interrupted: 'La sessione è terminata. Accedi di nuovo per riprendere da dove eri.',
      returningTo: 'Tornerai alla pagina che avevi chiesto.',
    },
    signUp: {
      title: 'Crea un account',
      submit: 'Crea account',
      submitting: 'Creazione in corso…',
      haveAccount: 'Hai già un account?',
      haveAccountLink: 'Accedi',
      acceptedTitle: 'Controlla la posta',
      acceptedBody: 'Se l\'indirizzo è utilizzabile, abbiamo inviato un\'email con le istruzioni.',
      acceptedSignIn: 'Vai all\'accesso',
    },
    fields: {
      email: 'Email',
      password: 'Password',
      fullName: 'Nome e cognome',
      passwordHint: 'Almeno 12 caratteri.',
    },
    errors: {
      credentials: 'Email o password non corretti.',
      tooManyAttempts: 'Troppi tentativi. Aspetta qualche minuto e riprova.',
      suspended: 'Questo account è sospeso. Contatta l\'assistenza.',
      invalidEmail: 'Questo indirizzo email non è valido.',
      weakPassword: 'Questa password non rispetta il requisito qui sopra.',
      unexpected: 'Non è stato possibile completare la richiesta. Riprova fra un momento.',
      required: 'Compila questo campo.',
    },
  },
}
