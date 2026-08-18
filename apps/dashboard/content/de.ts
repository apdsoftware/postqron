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
    account: {
      open: 'Kontomenü',
      signedInAs: 'Angemeldet als',
      signOut: 'Abmelden',
    },
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

  auth: {
    signIn: {
      title: 'Anmelden',
      submit: 'Anmelden',
      submitting: 'Anmeldung läuft…',
      noAccount: 'Noch kein Konto?',
      noAccountLink: 'Jetzt erstellen',
      interrupted: 'Deine Sitzung ist beendet. Melde dich erneut an, um dort weiterzumachen, wo du warst.',
      returningTo: 'Du kommst zurück auf die Seite, die du aufgerufen hattest.',
    },
    signUp: {
      title: 'Konto erstellen',
      submit: 'Konto erstellen',
      submitting: 'Wird erstellt…',
      haveAccount: 'Du hast schon ein Konto?',
      haveAccountLink: 'Anmelden',
      acceptedTitle: 'Sieh in deinem Postfach nach',
      acceptedBody: 'Falls die Adresse verwendbar ist, haben wir eine E-Mail mit den nächsten Schritten geschickt.',
      acceptedSignIn: 'Zur Anmeldung',
    },
    fields: {
      email: 'E-Mail',
      password: 'Passwort',
      fullName: 'Vor- und Nachname',
      passwordHint: 'Mindestens 12 Zeichen.',
    },
    errors: {
      credentials: 'E-Mail oder Passwort sind nicht korrekt.',
      tooManyAttempts: 'Zu viele Versuche. Warte ein paar Minuten und versuche es erneut.',
      suspended: 'Dieses Konto ist gesperrt. Wende dich an den Support.',
      invalidEmail: 'Diese E-Mail-Adresse ist ungültig.',
      weakPassword: 'Dieses Passwort erfüllt die Anforderung oben nicht.',
      unexpected: 'Die Anfrage konnte nicht abgeschlossen werden. Versuche es gleich noch einmal.',
      required: 'Fülle dieses Feld aus.',
    },
  },
}
