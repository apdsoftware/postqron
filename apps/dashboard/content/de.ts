import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in tedesco, tradotti da `content/en.ts`. */
export const de: DashboardContent = {
  shell: {
    languageLabel: 'Sprache',
  },

  home: {
    title: 'Übersicht',
    intro:
      'Grundgerüst des Monorepos. Die Flowbite-Vorlage, die Anmeldung und die '
      + 'Verwaltung der Cronjobs kommen mit eigenen Issues.',
    backendTitle: 'Backend',
    apiBaseLabel: 'Basisadresse der API',
    check: 'Backend-Status prüfen',
    checking: 'Wird geprüft…',
    unreachable: 'Backend nicht erreichbar',
  },
}
