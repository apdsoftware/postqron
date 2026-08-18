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
    account: {
      open: 'Menu du compte',
      signedInAs: 'Connecté en tant que',
      signOut: 'Se déconnecter',
    },
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

  auth: {
    signIn: {
      title: 'Se connecter',
      submit: 'Se connecter',
      submitting: 'Connexion en cours…',
      noAccount: 'Pas encore de compte ?',
      noAccountLink: 'Créez-en un',
      interrupted: 'Votre session a pris fin. Reconnectez-vous pour reprendre où vous en étiez.',
      returningTo: 'Vous reviendrez à la page que vous aviez demandée.',
    },
    signUp: {
      title: 'Créer un compte',
      submit: 'Créer le compte',
      submitting: 'Création en cours…',
      haveAccount: 'Vous avez déjà un compte ?',
      haveAccountLink: 'Connectez-vous',
      acceptedTitle: 'Consultez votre boîte mail',
      acceptedBody: 'Si l\'adresse est utilisable, nous avons envoyé un e-mail avec la marche à suivre.',
      acceptedSignIn: 'Aller à la connexion',
    },
    fields: {
      email: 'E-mail',
      password: 'Mot de passe',
      fullName: 'Nom et prénom',
      passwordHint: 'Au moins 12 caractères.',
    },
    errors: {
      credentials: 'L\'e-mail ou le mot de passe est incorrect.',
      tooManyAttempts: 'Trop de tentatives. Patientez quelques minutes, puis réessayez.',
      suspended: 'Ce compte est suspendu. Contactez l\'assistance.',
      invalidEmail: 'Cette adresse e-mail n\'est pas valide.',
      weakPassword: 'Ce mot de passe ne respecte pas l\'exigence ci-dessus.',
      unexpected: 'La requête n\'a pas pu aboutir. Réessayez dans un instant.',
      required: 'Remplissez ce champ.',
    },
  },
}
