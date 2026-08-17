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

  home: {
    title: 'Übersicht',
    intro: 'Der Postqron-Dienst, der deine Cronjobs ausführt — und ob er antwortet.',
    backendTitle: 'Zustand des Dienstes',
    apiBaseLabel: 'Basisadresse der API',
    check: 'Backend-Status prüfen',
    checking: 'Wird geprüft…',
    unreachable: 'Backend nicht erreichbar',
  },

  notFound: {
    title: 'Seite nicht gefunden',
    intro: 'Diese Adresse gehört zu keiner Ansicht des Dashboards. Sie hat sich vielleicht geändert, oder der Link ist falsch.',
    back: 'Zurück zur Übersicht',
  },
}
