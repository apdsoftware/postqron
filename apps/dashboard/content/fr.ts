import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in francese, tradotti da `content/en.ts`. */
export const fr: DashboardContent = {
  shell: {
    languageLabel: 'Langue',
  },

  home: {
    title: 'Aperçu',
    intro:
      'Ossature du monorepo. Le modèle Flowbite, la connexion et la gestion des '
      + 'tâches planifiées arrivent avec leurs propres issues.',
    backendTitle: 'Backend',
    apiBaseLabel: 'Adresse de base de l\'API',
    check: 'Vérifier l\'état du backend',
    checking: 'Vérification…',
    unreachable: 'Backend injoignable',
  },
}
