import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in italiano, tradotti da `content/en.ts`. */
export const it: DashboardContent = {
  shell: {
    languageLabel: 'Lingua',
  },

  home: {
    title: 'Panoramica',
    intro:
      'Scaffold del monorepo. Il template Flowbite, l\'accesso e la gestione dei '
      + 'cronjob arrivano con le issue dedicate.',
    backendTitle: 'Backend',
    apiBaseLabel: 'Indirizzo base dell\'API',
    check: 'Verifica lo stato del backend',
    checking: 'Verifica in corso…',
    unreachable: 'Backend non raggiungibile',
  },
}
