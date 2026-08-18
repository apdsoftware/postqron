import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in tedesco, tradotti da `content/en.ts`. */
export const de: DashboardContent = {
  shell: {
    languageLabel: 'Sprache',
    skipToContent: 'Zum Inhalt springen',
    navigationLabel: 'Hauptnavigation',
    openNavigation: 'Navigation öffnen',
    closeNavigation: 'Navigation schließen',
    nav: {
      overview: 'Übersicht',
    },
    toLightTheme: 'Zum hellen Design wechseln',
    toDarkTheme: 'Zum dunklen Design wechseln',
  },

  status: {
    loading: 'Wird geladen…',
    errorTitle: 'Etwas ist schiefgelaufen',
    retry: 'Erneut versuchen',
    errors: {
      network: 'Das Backend hat nicht geantwortet. Prüfe die Verbindung und versuche es erneut.',
      unauthorized: 'Deine Sitzung ist abgelaufen. Melde dich erneut an, um fortzufahren.',
      forbidden: 'Du hast keinen Zugriff darauf.',
      notFound: 'Das ist nicht mehr vorhanden.',
      invalid: 'Die Anfrage wurde abgelehnt. Prüfe deine Eingaben.',
      server: 'Im Backend ist ein Problem aufgetreten. Versuche es gleich noch einmal.',
    },
  },

  home: {
    title: 'Übersicht',
    intro: 'Der Postqron-Dienst, der deine Cronjobs ausführt — und ob er antwortet.',
    backendTitle: 'Zustand des Dienstes',
    apiBaseLabel: 'Basisadresse der API',
    statusLabel: 'Status',
    environmentLabel: 'Umgebung',
    versionLabel: 'Version',
    check: 'Erneut prüfen',
  },

  notFound: {
    title: 'Seite nicht gefunden',
    intro: 'Diese Adresse gehört zu keiner Ansicht des Dashboards. Sie hat sich vielleicht geändert, oder der Link ist falsch.',
    back: 'Zurück zur Übersicht',
  },
}
