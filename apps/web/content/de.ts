import type { SiteContent } from '~/types/content'

/**
 * Contenuti del sito pubblico in tedesco, tradotti da `content/en.ts`.
 *
 * I nomi dei piani, i prezzi e i nomi propri restano quelli della lingua
 * sorgente: sono identificatori commerciali, non testo.
 */
export const de: SiteContent = {
  meta: {
    title: 'Verlässliche Cronjobs, als Code definiert',
    description:
      'Beschreibe deine Zeitpläne in einer Datei in deinem Repository. Postqron führt '
      + 'sie pünktlich aus, wiederholt sie bei Bedarf und sagt dir immer, wie es lief.',
  },

  ui: {
    menu: 'Menü',
    homeLink: 'Postqron, zurück zur Startseite',
    language: 'Sprache',
    emailPlaceholder: 'E-Mail-Adresse eingeben',
    emailSubmit: 'Loslegen',
    readMore: 'Lesen',
    closeVideo: 'Schließen',
    photoOf: 'Foto von {name}',
    contactTitle: 'Kontakt',
    emailPrefix: 'E-Mail: ',
    rightsReserved: 'Alle Rechte vorbehalten.',
  },

  legal: {
    sourceNotice: 'Dieses Dokument ist noch nicht auf Deutsch verfügbar. Unten wird das englische Original angezeigt.',
    versionLabel: 'Version',
    effectiveDateLabel: 'Gültig ab',
  },

  nav: {
    main: [
      { label: 'Start', to: '/#welcome' },
      {
        label: 'Produkt',
        children: [
          { label: 'Funktionen', to: '/#features' },
          { label: 'Stimmen', to: '/#testimonials' },
          { label: 'Preise', to: '/#pricing' },
        ],
      },
      {
        label: 'Ressourcen',
        children: [
          { label: 'API und Webhooks', to: '/#api' },
          { label: 'Aus dem Blog', to: '/#blog' },
        ],
      },
      { label: 'Kontakt', to: '/#contact' },
    ],
    cta: { label: 'Kostenlos testen', to: '/#welcome' },
    footer: [
      {
        title: 'Produkt',
        items: [
          { label: 'Funktionen', to: '/#features' },
          { label: 'Preise', to: '/#pricing' },
          { label: 'API und Webhooks', to: '/#api' },
          { label: 'Blog', to: '/#blog' },
        ],
      },
      {
        title: 'Support',
        items: [
          { label: 'Stimmen', to: '/#testimonials' },
          { label: 'Kontakt', to: '/#contact' },
        ],
      },
      {
        title: 'Rechtliches',
        items: [
          { label: 'Nutzungsbedingungen', to: '/legal/terms-of-service' },
          { label: 'Datenschutzerklärung', to: '/legal/privacy-policy' },
          { label: 'Cookie-Richtlinie', to: '/legal/cookie-policy' },
          { label: 'Zulässige Nutzung', to: '/legal/acceptable-use-policy' },
        ],
      },
    ],
  },

  company: {
    name: 'Postqron',
    legalName: 'Postqron',
    about:
      'Verwaltete Cronjobs, in deinem eigenen Repository definiert und an einem Ort '
      + 'zusammengeführt: verlässliche Zeitpläne, ein Protokoll jeder Ausführung und '
      + 'eine Meldung, wenn etwas nicht startet.',
    address: 'Anschrift folgt',
    email: 'support@postqron.com',
  },

  // Im Deutschen folgt das Symbol der Zahl, mit Leerzeichen: «9 € + MwSt.».
  money: { currencyPosition: 'after', taxNote: '+ MwSt.' },

  hero: {
    title: 'Verlässliche Cronjobs, als Code definiert',
    text:
      'Beschreibe deine Zeitpläne in einer Datei in deinem Repository. Postqron führt '
      + 'sie pünktlich aus, wiederholt sie bei Bedarf und sagt dir immer, wie es lief.',
    image: '/img/hero.jpg',
    imageAlt: 'Die Postqron-Konsole mit der Liste der Ausführungen',
  },

  featuresIntro: {
    title: 'Vier Dinge, die Postqron von deiner Liste nimmt',
    lead:
      'Kein Cron-Server, den du am Leben halten musst, kein Skript, das stillschweigend '
      + 'scheitert, keine Zweifel daran, was wann gelaufen ist.',
  },

  features: [
    {
      icon: 'checkSquare',
      title: 'Alle Jobs an einem Ort',
      text: 'Verschiedene Repositories und Umgebungen, eine Liste.',
    },
    {
      icon: 'github',
      title: 'Im Repository definiert',
      text: 'Eine cron.yaml, bei jedem Push erneut gelesen.',
      highlighted: true,
    },
    {
      icon: 'barChart',
      title: 'Protokoll jeder Ausführung',
      text: 'Dauer, Ergebnis und Antwort, während sie entstehen.',
    },
    {
      icon: 'bell',
      title: 'Wiederholen und melden',
      text: 'Backoff bei Fehlern, Meldung, wenn das nicht reicht.',
    },
  ],

  showcases: [
    {
      title: 'Deine Zeitpläne leben in deinem Repository',
      text:
        'Eine cron.yaml beschreibt Jobs, Zeiten und Ziele. Bei jedem Push liest '
        + 'Postqron sie neu und gleicht alles ab: die Prüfung läuft über den Pull '
        + 'Request.',
      bullets: [
        'Cron-Ausdrücke mit Zeitzone, Sommerzeit inklusive',
        'Intervallmodus bis auf die Sekunde',
        'Getrennte Umgebungen für Staging und Produktion',
        'Syntaxfehler direkt am Commit gemeldet',
      ],
      image: '/img/screenshots/jobs.png',
      imageAlt: 'Liste der aus einem Repository synchronisierten Cronjobs',
      imageWidth: 593,
      imageHeight: 467,
      imageSide: 'left',
    },
    {
      title: 'Du weißt immer, wie es gelaufen ist',
      text:
        'Jede Ausführung hinterlässt eine Spur: wann sie startete, wie lange sie '
        + 'dauerte, was sie antwortete. Fehler werden zur Meldung, nicht zur Entdeckung.',
      bullets: [
        'Verlauf der Ausführungen, filterbar nach Ergebnis',
        'Durchschnittsdauer und Fehlerquote pro Job',
        'Meldungen per E-Mail oder Webhook an Slack und Discord',
      ],
      image: '/img/screenshots/metrics.png',
      imageAlt: 'Diagramm der Ausführungsdauer im Zeitverlauf',
      imageWidth: 605,
      imageHeight: 375,
      imageSide: 'right',
    },
  ],

  apiBand: {
    text: 'Dokumentation zu API, Kommandozeile und Webhooks. Such dir aus, wo du anfängst.',
    channels: [
      { icon: 'code', label: 'REST-API', to: '/#api' },
      { icon: 'terminal', label: 'CLI', to: '/#api' },
      { icon: 'plug', label: 'Webhooks', to: '/#api' },
    ],
  },

  testimonialsIntro: {
    title: 'Wer damit arbeitet',
    lead:
      'Teams, die keine Maschine mehr am Leben halten, nur damit darauf ein paar Zeilen '
      + 'crontab laufen.',
  },

  testimonials: [
    {
      name: 'Giulia Tomassini',
      role: 'Backend Lead',
      quote:
        'Wir hatten drei Server mit drei verschiedenen Crontabs und niemand wusste mehr, '
        + 'welcher der richtige war. Jetzt steht die Wahrheit im Repository und wir '
        + 'prüfen sie im Pull Request.',
      avatar: '/img/people/1.svg',
      placeholder: true,
    },
    {
      name: 'Marco Renzi',
      role: 'Gründer',
      quote:
        'Der nächtliche Abrechnungsjob war zwei Wochen lang ausgefallen, ohne dass es '
        + 'jemandem auffiel. Jetzt kommt beim zweiten Fehlversuch eine Meldung.',
      avatar: '/img/people/2.svg',
      placeholder: true,
    },
    {
      name: 'Sara Lombardi',
      role: 'Platform Engineer',
      quote:
        'Der Intervallmodus hat uns einen ganzen Dienst erspart: was alle zehn Sekunden '
        + 'in eine Queue schrieb, macht jetzt Postqron.',
      avatar: '/img/people/3.svg',
      placeholder: true,
    },
    {
      name: 'Andrea Cesaroni',
      role: 'CTO',
      quote:
        'Staging und Produktion haben unterschiedliche Zeitpläne, und das ist endlich '
        + 'keine Umgebungsvariable mehr, an die jemand von Hand denken muss.',
      avatar: '/img/people/4.svg',
      placeholder: true,
    },
    {
      name: 'Davide Ferraro',
      role: 'Selbstständiger Entwickler',
      quote:
        'Der kostenlose Tarif deckt alle meine Nebenprojekte ab. Das erste umzuziehen '
        + 'hat mich zehn Minuten gekostet.',
      avatar: '/img/people/5.svg',
      placeholder: true,
    },
    {
      name: 'Elena Nardi',
      role: 'Produktverantwortliche',
      quote:
        'In die Ausführungsprotokolle schauen auch die, die keinen Code anfassen: es ist '
        + 'das Erste, was wir öffnen, wenn ein Report ausbleibt.',
      avatar: '/img/people/6.svg',
      placeholder: true,
    },
  ],

  pricingIntro: {
    title: 'Tarife',
    lead:
      'Kostenlos anfangen und wechseln, wenn es nötig wird. Die Grenzen setzt die '
      + 'Engine durch, sie stehen nicht nur hier.',
  },

  plans: [
    {
      name: 'Free',
      currency: '€',
      price: '0',
      period: '/Monat',
      ctaLabel: 'Kostenlos starten',
      ctaTo: '/#welcome',
      features: [
        { label: '20 Cronjobs', included: true },
        { label: 'Auflösung von 1 Minute', included: true },
        { label: '3 Tage Protokolle', included: true },
        { label: '1 cron.yaml-Repository', included: true },
        { label: 'Meldungen per E-Mail', included: true },
        { label: 'Staging und Produktion', included: false },
        { label: 'KI-Debugging mit eigenem Schlüssel', included: false },
        { label: 'Rollen und Berechtigungen', included: false },
        { label: 'Isolierte Workspaces und feste IP', included: false },
      ],
    },
    {
      name: 'Pro',
      currency: '€',
      price: '9',
      period: '/Monat',
      featured: true,
      ctaLabel: 'Pro wählen',
      ctaTo: '/#welcome',
      features: [
        { label: '200 Cronjobs', included: true },
        { label: 'Auflösung von 10 Sekunden', included: true },
        { label: '15 Tage Protokolle', included: true },
        { label: 'Unbegrenzte Repositories', included: true },
        { label: 'Meldungen in Slack und Discord', included: true },
        { label: 'Staging und Produktion', included: true },
        { label: 'KI-Debugging mit eigenem Schlüssel', included: true },
        { label: 'Rollen und Berechtigungen', included: false },
        { label: 'Isolierte Workspaces und feste IP', included: false },
      ],
    },
    {
      name: 'Team',
      currency: '€',
      price: '29',
      period: '/Monat',
      ctaLabel: 'Team wählen',
      ctaTo: '/#welcome',
      features: [
        { label: 'Unbegrenzte Cronjobs', included: true },
        { label: 'Auflösung von 1 Sekunde', included: true },
        { label: '30 Tage Protokolle, mit Export', included: true },
        { label: 'Unbegrenzte Repositories', included: true },
        { label: 'Meldungen pro Mitglied und Umgebung', included: true },
        { label: 'Staging und Produktion', included: true },
        { label: 'KI-Debugging mit eigenem Schlüssel', included: true },
        { label: 'Rollen und Berechtigungen', included: true },
        { label: 'Isolierte Workspaces und feste IP', included: false },
      ],
    },
    {
      name: 'Agency',
      pricePrefix: 'ab',
      currency: '€',
      price: '79',
      period: '/Monat',
      ctaLabel: 'Sprich mit uns',
      ctaTo: '/#contact',
      features: [
        { label: 'Unbegrenzte Cronjobs', included: true },
        { label: 'Auflösung von 1 Sekunde', included: true },
        { label: '90 Tage Protokolle, mit Export', included: true },
        { label: 'Unbegrenzte Repositories', included: true },
        { label: 'Meldungen pro Mitglied und Umgebung', included: true },
        { label: 'Staging und Produktion', included: true },
        { label: 'KI-Debugging mit eigenem Schlüssel', included: true },
        { label: 'Rollen und Berechtigungen', included: true },
        { label: 'Isolierte Workspaces und feste IP', included: true },
      ],
    },
  ],

  stats: [
    { value: 1, label: 'Sekunde\nAuflösung' },
    { value: 3, label: 'Versuche\nstandardmäßig' },
    { value: 30, label: 'Sekunden\nZeitlimit' },
    { value: 90, label: 'Tage\nAufbewahrung' },
  ],

  blogIntro: {
    title: 'Aus dem Blog',
    lead: 'Notizen über Zeitpläne, Verlässlichkeit und das Handwerk, Dinge pünktlich zu starten.',
  },

  articles: [
    {
      title: 'Warum dich ein Cron auf einer einzigen Maschine früher oder später im Stich lässt',
      excerpt:
        'Neustarts, Zeitzonen und Sommerzeit: die drei Arten, wie ein Zeitplan, der '
        + 'einfach aussah, aufhört zu laufen, ohne es jemandem zu sagen.',
      image: '/img/blog/1.jpg',
      to: '/#blog',
    },
    {
      title: 'Was in die cron.yaml gehört und was in den Code',
      excerpt:
        'Der Zeitplan ist Konfiguration, die Arbeit nicht. Wo die Grenze verläuft und '
        + 'warum es sich lohnt, sie ab dem ersten Job scharf zu halten.',
      image: '/img/blog/2.jpg',
      to: '/#blog',
    },
    {
      title: 'Richtig wiederholen: Backoff, Idempotenz und sinnvolle Grenzen',
      excerpt:
        'Ein Versuch mehr kann eine kurze Störung beheben oder eine Abbuchung '
        + 'verdoppeln. Wie man für jeden Job die richtige Strategie wählt.',
      image: '/img/blog/3.jpg',
      to: '/#blog',
    },
  ],
}
