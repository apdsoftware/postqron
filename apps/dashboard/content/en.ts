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

  status: {
    loading: 'Loading…',
    errorTitle: 'Something went wrong',
    retry: 'Try again',
    errors: {
      network: 'The backend did not answer. Check your connection, then try again.',
      unauthorized: 'Your session has expired. Sign in again to continue.',
      forbidden: 'You do not have access to this.',
      notFound: 'This is no longer here.',
      invalid: 'The request was rejected. Check what you entered.',
      server: 'The backend ran into a problem. Try again in a moment.',
    },
  },

  home: {
    title: 'Overview',
    intro: 'The Postqron service that runs your cron jobs, and whether it is answering.',
    backendTitle: 'Service health',
    apiBaseLabel: 'API base URL',
    statusLabel: 'Status',
    environmentLabel: 'Environment',
    versionLabel: 'Version',
    check: 'Check again',
  },

  notFound: {
    title: 'Page not found',
    intro: 'This address matches no screen of the dashboard. It may have moved, or the link may be wrong.',
    back: 'Back to the overview',
  },
}
