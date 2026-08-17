import type { DashboardContent } from '~/types/content'

/**
 * Testi della dashboard in **inglese**, lingua sorgente (SPEC §8-bis).
 *
 * Le altre quattro lingue traducono da qui: si scrive in inglese e si traduce,
 * non il contrario. Chi cambia un testo lo cambia prima in questo file, poi
 * negli altri.
 */
export const en: DashboardContent = {
  shell: {
    languageLabel: 'Language',
    skipToContent: 'Skip to content',
    navigationLabel: 'Main navigation',
    openNavigation: 'Open navigation',
    closeNavigation: 'Close navigation',
    nav: {
      overview: 'Overview',
    },
    toLightTheme: 'Switch to light theme',
    toDarkTheme: 'Switch to dark theme',
  },

  home: {
    title: 'Overview',
    intro: 'The Postqron service that runs your cron jobs, and whether it is answering.',
    backendTitle: 'Service health',
    apiBaseLabel: 'API base URL',
    check: 'Run health check',
    checking: 'Checking…',
    unreachable: 'Backend unreachable',
  },

  notFound: {
    title: 'Page not found',
    intro: 'This address matches no screen of the dashboard. It may have moved, or the link may be wrong.',
    back: 'Back to the overview',
  },
}
