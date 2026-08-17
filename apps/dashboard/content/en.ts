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
  },

  home: {
    title: 'Overview',
    intro:
      'Monorepo scaffold. The Flowbite template, sign-in and cron job management '
      + 'arrive with their own issues.',
    backendTitle: 'Backend',
    apiBaseLabel: 'API base URL',
    check: 'Run health check',
    checking: 'Checking…',
    unreachable: 'Backend unreachable',
  },
}
