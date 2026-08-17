import type { DashboardContent } from '~/types/content'

/** Testi della dashboard in francese, tradotti da `content/en.ts`. */
export const fr: DashboardContent = {
  shell: {
    languageLabel: 'Langue',
    skipToContent: 'Aller au contenu',
    navigationLabel: 'Navigation principale',
    openNavigation: 'Ouvrir la navigation',
    closeNavigation: 'Fermer la navigation',
    nav: {
      overview: 'Aperçu',
    },
    toLightTheme: 'Passer au thème clair',
    toDarkTheme: 'Passer au thème sombre',
  },

  home: {
    title: 'Aperçu',
    intro: 'Le service Postqron qui exécute vos tâches planifiées, et s\'il répond.',
    backendTitle: 'État du service',
    apiBaseLabel: 'Adresse de base de l\'API',
    check: 'Vérifier l\'état du backend',
    checking: 'Vérification…',
    unreachable: 'Backend injoignable',
  },

  notFound: {
    title: 'Page introuvable',
    intro: 'Cette adresse ne correspond à aucun écran du tableau de bord. Elle a peut-être changé, ou le lien est erroné.',
    back: 'Retour à l\'aperçu',
  },
}
