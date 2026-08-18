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

  status: {
    loading: 'Chargement…',
    errorTitle: 'Quelque chose n\'a pas fonctionné',
    retry: 'Réessayer',
    errors: {
      network: 'Le backend n\'a pas répondu. Vérifiez la connexion, puis réessayez.',
      unauthorized: 'Votre session a expiré. Reconnectez-vous pour continuer.',
      forbidden: 'Vous n\'avez pas accès à cette ressource.',
      notFound: 'Cette ressource n\'existe plus.',
      invalid: 'La requête a été refusée. Vérifiez les données saisies.',
      server: 'Le backend a rencontré un problème. Réessayez dans un instant.',
    },
  },

  home: {
    title: 'Aperçu',
    intro: 'Le service Postqron qui exécute vos tâches planifiées, et s\'il répond.',
    backendTitle: 'État du service',
    apiBaseLabel: 'Adresse de base de l\'API',
    statusLabel: 'État',
    environmentLabel: 'Environnement',
    versionLabel: 'Version',
    check: 'Vérifier à nouveau',
  },

  notFound: {
    title: 'Page introuvable',
    intro: 'Cette adresse ne correspond à aucun écran du tableau de bord. Elle a peut-être changé, ou le lien est erroné.',
    back: 'Retour à l\'aperçu',
  },
}
