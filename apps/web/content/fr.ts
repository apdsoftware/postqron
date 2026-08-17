import type { SiteContent } from '~/types/content'

/**
 * Contenuti del sito pubblico in francese, tradotti da `content/en.ts`.
 *
 * I nomi dei piani, i prezzi e i nomi propri restano quelli della lingua
 * sorgente: sono identificatori commerciali, non testo.
 */
export const fr: SiteContent = {
  meta: {
    title: 'Des tâches cron fiables, définies comme du code',
    description:
      'Décrivez vos planifications dans un fichier de votre dépôt. Postqron les exécute '
      + 'à l\'heure, réessaie quand il le faut et vous dit toujours comment ça s\'est passé.',
  },

  ui: {
    menu: 'Menu',
    homeLink: 'Postqron, retour à l\'accueil',
    language: 'Langue',
    emailPlaceholder: 'Saisissez votre e-mail',
    emailSubmit: 'Commencer',
    readMore: 'Lire',
    closeVideo: 'Fermer',
    photoOf: 'Photo de {name}',
    contactTitle: 'Contact',
    emailPrefix: 'E-mail : ',
    rightsReserved: 'Tous droits réservés.',
    cookiePreferences: 'Préférences des cookies',
  },

  cookieBanner: {
    title: 'Votre choix de cookies',
    description: 'Nous utilisons des cookies nécessaires au fonctionnement du site. Les technologies facultatives restent bloquées sans votre accord.',
    accept: 'Accepter les cookies facultatifs',
    reject: 'Refuser les cookies facultatifs',
    policyLink: 'Lire la Politique relative aux cookies',
  },

  legal: {
    sourceNotice: 'Ce document n’est pas encore disponible en français. L’original anglais est présenté ci-dessous.',
    versionLabel: 'Version',
    effectiveDateLabel: 'Date d’entrée en vigueur',
  },

  nav: {
    main: [
      { label: 'Accueil', to: '/#welcome' },
      {
        label: 'Produit',
        children: [
          { label: 'Fonctionnalités', to: '/features' },
          { label: 'Témoignages', to: '/#testimonials' },
          { label: 'Tarifs', to: '/pricing' },
        ],
      },
      {
        label: 'Ressources',
        children: [
          { label: 'API et webhooks', to: '/#api' },
          { label: 'Du blog', to: '/#blog' },
        ],
      },
      { label: 'FAQ', to: '/faq' },
      { label: 'Contact', to: '/contact' },
    ],
    cta: { label: 'Créer un compte Free', to: '/#welcome' },
    footer: [
      {
        title: 'Produit',
        items: [
          { label: 'Fonctionnalités', to: '/features' },
          { label: 'Tarifs', to: '/pricing' },
          { label: 'API et webhooks', to: '/#api' },
          { label: 'Blog', to: '/#blog' },
        ],
      },
      {
        title: 'Assistance',
        items: [
          { label: 'Témoignages', to: '/#testimonials' },
          { label: 'FAQ', to: '/faq' },
          { label: 'Contact', to: '/contact' },
        ],
      },
      {
        title: 'Mentions légales',
        items: [
          { label: 'Conditions d’utilisation', to: '/legal/terms-of-service' },
          { label: 'Politique de confidentialité', to: '/legal/privacy-policy' },
          { label: 'Politique relative aux cookies', to: '/legal/cookie-policy' },
          { label: 'Utilisation acceptable', to: '/legal/acceptable-use-policy' },
        ],
      },
    ],
  },

  company: {
    name: 'Postqron',
    legalName: 'Apdsoftware di Carlo Zuffetti',
    about:
      'Des tâches cron gérées pour vous, définies dans votre propre dépôt et ramenées '
      + 'à un seul endroit : une planification fiable, le journal de chaque exécution '
      + 'et une alerte quand quelque chose ne démarre pas.',
    address: 'Via C. Colombo 15, 24047 Treviglio (BG), Italie · TVA 03835250162 · REA BG 431224',
    email: 'hello@postqron.com',
  },

  // En français le symbole suit le chiffre, avec une espace : «9 € + TVA».
  money: { currencyPosition: 'after', taxNote: '+ TVA' },

  hero: {
    title: 'Des tâches cron fiables, définies comme du code',
    text:
      'Décrivez vos planifications dans un fichier de votre dépôt. Postqron les exécute '
      + 'à l\'heure, réessaie quand il le faut et vous dit toujours comment ça s\'est passé.',
    image: '/img/hero.jpg',
    imageAlt: 'La console Postqron avec la liste des exécutions',
  },

  featuresIntro: {
    title: 'Quatre choses que Postqron raye de votre liste',
    lead:
      'Aucun serveur cron à maintenir en vie, aucun script qui échoue en silence, aucun '
      + 'doute sur ce qui s\'est exécuté et quand.',
  },

  features: [
    {
      icon: 'checkSquare',
      title: 'Toutes les tâches au même endroit',
      text: 'Des dépôts et des environnements différents, une seule liste.',
    },
    {
      icon: 'github',
      title: 'Définies dans votre dépôt',
      text: 'Un cron.yaml, relu à chaque push sur la branche.',
      highlighted: true,
    },
    {
      icon: 'barChart',
      title: 'Un journal de chaque exécution',
      text: 'Durée, résultat et réponse, au fil de l\'eau.',
    },
    {
      icon: 'bell',
      title: 'Réessaie et alerte',
      text: 'Backoff en cas de panne, alerte quand cela ne suffit pas.',
    },
  ],

  showcases: [
    {
      title: 'Vos planifications vivent dans votre dépôt',
      text:
        'Un fichier cron.yaml décrit les tâches, les horaires et les destinations. À '
        + 'chaque push, Postqron le relit et réaligne tout : la revue passe par la pull '
        + 'request.',
      bullets: [
        'Expressions cron avec fuseau horaire, heure d\'été comprise',
        'Mode par intervalle jusqu\'à la seconde',
        'Environnements séparés pour la préproduction et la production',
        'Erreurs de syntaxe signalées sur le commit',
      ],
      image: '/img/screenshots/jobs.png',
      imageAlt: 'Liste des tâches cron synchronisées depuis un dépôt',
      imageWidth: 593,
      imageHeight: 467,
      imageSide: 'left',
    },
    {
      title: 'Vous savez toujours comment ça s\'est passé',
      text:
        'Chaque occurrence laisse une trace : quand elle a démarré, combien de temps '
        + 'elle a duré, ce qu\'elle a répondu. Les échecs deviennent une alerte, pas une '
        + 'découverte.',
      bullets: [
        'Historique des exécutions filtrable par résultat',
        'Durée moyenne et taux d\'échec par tâche',
        'Alertes par e-mail ou webhook sur Slack et Discord',
      ],
      image: '/img/screenshots/metrics.png',
      imageAlt: 'Graphique de la durée des exécutions dans le temps',
      imageWidth: 605,
      imageHeight: 375,
      imageSide: 'right',
    },
  ],

  apiBand: {
    text: 'Documentation de l\'API, de la ligne de commande et des webhooks. Choisissez par où commencer.',
    channels: [
      { icon: 'code', label: 'API REST', to: '/#api' },
      { icon: 'terminal', label: 'CLI', to: '/#api' },
      { icon: 'plug', label: 'Webhooks', to: '/#api' },
    ],
  },

  testimonialsIntro: {
    title: 'Qui l\'utilise',
    lead:
      'Des équipes qui ont cessé de maintenir une machine en vie juste pour y faire '
      + 'tourner quelques lignes de crontab.',
  },

  testimonials: [
    {
      name: 'Giulia Tomassini',
      role: 'Backend lead',
      quote:
        'Nous avions trois serveurs avec trois crontab différents et plus personne ne '
        + 'savait lequel était le bon. Maintenant la vérité est dans le dépôt et nous la '
        + 'relisons en pull request.',
      avatar: '/img/people/1.svg',
      placeholder: true,
    },
    {
      name: 'Marco Renzi',
      role: 'Fondateur',
      quote:
        'La tâche de facturation nocturne était en échec depuis deux semaines sans que '
        + 'personne s\'en aperçoive. Désormais une alerte arrive au deuxième essai raté.',
      avatar: '/img/people/2.svg',
      placeholder: true,
    },
    {
      name: 'Sara Lombardi',
      role: 'Platform engineer',
      quote:
        'Le mode par intervalle nous a enlevé un service entier : ce qui écrivait dans '
        + 'une file toutes les dix secondes, c\'est Postqron qui le fait.',
      avatar: '/img/people/3.svg',
      placeholder: true,
    },
    {
      name: 'Andrea Cesaroni',
      role: 'CTO',
      quote:
        'La préproduction et la production ont des planifications différentes, et ce '
        + 'n\'est enfin plus une variable d\'environnement à retenir à la main.',
      avatar: '/img/people/4.svg',
      placeholder: true,
    },
    {
      name: 'Davide Ferraro',
      role: 'Développeur indépendant',
      quote:
        'L\'offre gratuite couvre tous mes projets personnels. Migrer le premier m\'a '
        + 'pris dix minutes.',
      avatar: '/img/people/5.svg',
      placeholder: true,
    },
    {
      name: 'Elena Nardi',
      role: 'Responsable produit',
      quote:
        'Les journaux d\'exécution sont lus aussi par ceux qui ne touchent pas au code : '
        + 'c\'est la première chose que nous ouvrons quand un rapport n\'arrive pas.',
      avatar: '/img/people/6.svg',
      placeholder: true,
    },
  ],

  pricingIntro: {
    title: 'Offres',
    lead:
      'On commence gratuitement et on change quand il le faut. Les limites sont '
      + 'appliquées par le moteur, pas seulement écrites ici.',
  },

  plans: [
    {
      name: 'Free',
      currency: '€',
      price: '0',
      period: '/mois',
      ctaLabel: 'Commencer gratuitement',
      ctaTo: '/#welcome',
      features: [
        { label: '20 tâches cron', included: true },
        { label: 'Résolution d\'une minute', included: true },
        { label: '3 jours de journaux', included: true },
        { label: '1 dépôt cron.yaml', included: true },
        { label: 'Alertes par e-mail', included: true },
        { label: 'Préproduction et production', included: false },
        { label: 'Débogage IA avec votre clé', included: false },
        { label: 'Rôles et permissions', included: false },
        { label: 'Espaces isolés et IP dédiée', included: false },
      ],
    },
    {
      name: 'Pro',
      currency: '€',
      price: '9',
      period: '/mois',
      annual: { price: '90', period: '/an', savingNote: 'Deux mois offerts' },
      featured: true,
      ctaLabel: 'Nous contacter pour Pro',
      ctaTo: '/contact',
      features: [
        { label: '200 tâches cron', included: true },
        { label: 'Résolution de 10 secondes', included: true },
        { label: '15 jours de journaux', included: true },
        { label: 'Dépôts illimités', included: true },
        { label: 'Alertes sur Slack et Discord', included: true },
        { label: 'Préproduction et production', included: true },
        { label: 'Débogage IA avec votre clé', included: true },
        { label: 'Rôles et permissions', included: false },
        { label: 'Espaces isolés et IP dédiée', included: false },
      ],
    },
    {
      name: 'Team',
      currency: '€',
      price: '29',
      period: '/mois',
      ctaLabel: 'Nous contacter pour Team',
      ctaTo: '/contact',
      features: [
        { label: 'Tâches cron illimitées', included: true },
        { label: 'Résolution d\'une seconde', included: true },
        { label: '30 jours de journaux, avec export', included: true },
        { label: 'Dépôts illimités', included: true },
        { label: 'Alertes par membre et environnement', included: true },
        { label: 'Préproduction et production', included: true },
        { label: 'Débogage IA avec votre clé', included: true },
        { label: 'Rôles et permissions', included: true },
        { label: 'Espaces isolés et IP dédiée', included: false },
      ],
    },
    {
      name: 'Agency',
      pricePrefix: 'dès',
      currency: '€',
      price: '79',
      period: '/mois',
      ctaLabel: 'Parlons-en',
      ctaTo: '/contact',
      features: [
        { label: 'Tâches cron illimitées', included: true },
        { label: 'Résolution d\'une seconde', included: true },
        { label: '90 jours de journaux, avec export', included: true },
        { label: 'Dépôts illimités', included: true },
        { label: 'Alertes par membre et environnement', included: true },
        { label: 'Préproduction et production', included: true },
        { label: 'Débogage IA avec votre clé', included: true },
        { label: 'Rôles et permissions', included: true },
        { label: 'Espaces isolés et IP dédiée', included: true },
      ],
    },
  ],

  stats: [
    { value: 1, label: 'Seconde de\nrésolution' },
    { value: 3, label: 'Tentatives par\ndéfaut' },
    { value: 30, label: 'Secondes de\ndélai' },
    { value: 90, label: 'Jours de\nrétention' },
  ],

  blogIntro: {
    title: 'Du blog',
    lead: 'Des notes sur la planification, la fiabilité et le métier de faire partir les choses à l\'heure.',
  },

  articles: [
    {
      title: 'Pourquoi un cron sur une seule machine finit par vous trahir',
      excerpt:
        'Redémarrages, fuseaux horaires et heure d\'été : les trois façons dont une '
        + 'planification qui semblait simple cesse de partir sans le dire à personne.',
      image: '/img/blog/1.jpg',
      to: '/#blog',
    },
    {
      title: 'Ce qui va dans cron.yaml, et ce qui reste dans le code',
      excerpt:
        'La planification est de la configuration, le travail non. Où passe la frontière '
        + 'et pourquoi il vaut mieux la garder nette dès la première tâche.',
      image: '/img/blog/2.jpg',
      to: '/#blog',
    },
    {
      title: 'Bien réessayer : backoff, idempotence et limites raisonnables',
      excerpt:
        'Une tentative de plus peut régler une panne passagère ou doubler un débit. '
        + 'Comment choisir la bonne politique pour chaque tâche.',
      image: '/img/blog/3.jpg',
      to: '/#blog',
    },
  ],
}
