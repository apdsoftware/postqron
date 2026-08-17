import type { SiteContent } from '~/types/content'

/**
 * Contenuti del sito pubblico in italiano, tradotti da `content/en.ts`.
 *
 * I nomi dei piani, i prezzi e i nomi propri restano quelli della lingua
 * sorgente: sono identificatori commerciali, non testo.
 */
export const it: SiteContent = {
  meta: {
    title: 'Cronjob affidabili, definiti come codice',
    description:
      'Descrivi le schedulazioni in un file del tuo repository. Postqron le esegue '
      + 'all\'orario giusto, ritenta quando serve e ti dice sempre com\'è andata.',
  },

  ui: {
    menu: 'Menu',
    homeLink: 'Postqron, torna alla home',
    language: 'Lingua',
    emailPlaceholder: 'Inserisci la tua email',
    emailSubmit: 'Inizia ora',
    readMore: 'Leggi',
    closeVideo: 'Chiudi',
    photoOf: 'Foto di {name}',
    contactTitle: 'Contatti',
    emailPrefix: 'Email: ',
    rightsReserved: 'Tutti i diritti riservati.',
    cookiePreferences: 'Preferenze cookie',
  },

  cookieBanner: {
    title: 'La tua scelta sui cookie',
    description: 'Usiamo cookie necessari per far funzionare il sito. Le tecnologie facoltative restano bloccate salvo tua accettazione.',
    accept: 'Accetta cookie facoltativi',
    reject: 'Rifiuta cookie facoltativi',
    policyLink: 'Leggi la Cookie Policy',
  },

  legal: {
    sourceNotice: 'Questo documento non è ancora disponibile in italiano. Di seguito è mostrato l’originale inglese.',
    versionLabel: 'Versione',
    effectiveDateLabel: 'Data di entrata in vigore',
  },

  nav: {
    main: [
      { label: 'Home', to: '/#welcome' },
      {
        label: 'Prodotto',
        children: [
          { label: 'Funzionalità', to: '/features' },
          { label: 'Testimonianze', to: '/#testimonials' },
          { label: 'Prezzi', to: '/pricing' },
        ],
      },
      {
        label: 'Risorse',
        children: [
          { label: 'API e webhook', to: '/#api' },
          { label: 'Dal blog', to: '/#blog' },
        ],
      },
      { label: 'FAQ', to: '/faq' },
      { label: 'Contatti', to: '/contact' },
    ],
    cta: { label: 'Crea un account Free', to: '/#welcome' },
    footer: [
      {
        title: 'Prodotto',
        items: [
          { label: 'Funzionalità', to: '/features' },
          { label: 'Prezzi', to: '/pricing' },
          { label: 'API e webhook', to: '/#api' },
          { label: 'Blog', to: '/#blog' },
        ],
      },
      {
        title: 'Assistenza',
        items: [
          { label: 'Testimonianze', to: '/#testimonials' },
          { label: 'FAQ', to: '/faq' },
          { label: 'Contatti', to: '/contact' },
        ],
      },
      {
        title: 'Note legali',
        items: [
          { label: 'Termini di servizio', to: '/legal/terms-of-service' },
          { label: 'Informativa sulla privacy', to: '/legal/privacy-policy' },
          { label: 'Informativa sui cookie', to: '/legal/cookie-policy' },
          { label: 'Uso accettabile', to: '/legal/acceptable-use-policy' },
        ],
      },
    ],
  },

  company: {
    name: 'Postqron',
    legalName: 'Apdsoftware di Carlo Zuffetti',
    about:
      'Cronjob gestiti, definiti nel tuo repository e ricondotti a un solo posto: '
      + 'una schedulazione affidabile, i log di ogni esecuzione e un avviso quando '
      + 'qualcosa non parte.',
    address: 'Via C. Colombo 15, 24047 Treviglio (BG), Italia · P. IVA 03835250162 · REA BG 431224',
    email: 'hello@postqron.com',
  },

  // In italiano il simbolo segue la cifra, con spazio: «9 € + IVA».
  money: { currencyPosition: 'after', taxNote: '+ IVA' },

  hero: {
    title: 'Cronjob affidabili, definiti come codice',
    text:
      'Descrivi le schedulazioni in un file del tuo repository. Postqron le esegue '
      + 'all\'orario giusto, ritenta quando serve e ti dice sempre com\'è andata.',
    image: '/img/hero.jpg',
    imageAlt: 'La console di Postqron con l\'elenco delle esecuzioni',
  },

  featuresIntro: {
    title: 'Quattro cose che Postqron toglie dalla tua lista',
    lead:
      'Nessun server cron da tenere in piedi, nessuno script che fallisce in '
      + 'silenzio, nessun dubbio su cosa è stato eseguito e quando.',
  },

  features: [
    {
      icon: 'checkSquare',
      title: 'Tutti i job in un posto',
      text: 'Repository e ambienti diversi, un elenco solo.',
    },
    {
      icon: 'github',
      title: 'Definiti nel repository',
      text: 'Un cron.yaml, riletto a ogni push sul ramo.',
      highlighted: true,
    },
    {
      icon: 'barChart',
      title: 'Log di ogni esecuzione',
      text: 'Durata, esito e risposta, mentre accadono.',
    },
    {
      icon: 'bell',
      title: 'Ritenta e avvisa',
      text: 'Backoff sui guasti, notifica quando non basta.',
    },
  ],

  showcases: [
    {
      title: 'Le schedulazioni vivono nel tuo repository',
      text:
        'Un file cron.yaml descrive job, orari e destinazioni. A ogni push Postqron '
        + 'lo rilegge e riallinea tutto: la revisione passa dalla pull request.',
      bullets: [
        'Espressioni cron con fuso orario, ora legale compresa',
        'Modalità a intervallo fino al secondo',
        'Ambienti separati per staging e produzione',
        'Errori di sintassi segnalati sul commit',
      ],
      image: '/img/screenshots/jobs.png',
      imageAlt: 'Elenco dei cronjob sincronizzati da un repository',
      imageWidth: 593,
      imageHeight: 467,
      imageSide: 'left',
    },
    {
      title: 'Sai sempre com\'è andata',
      text:
        'Ogni occorrenza lascia una traccia: quando è partita, quanto è durata, cosa '
        + 'ha risposto. I fallimenti diventano un avviso, non una scoperta.',
      bullets: [
        'Cronologia delle esecuzioni con filtri per esito',
        'Durata media e tasso di fallimento per job',
        'Avvisi via email o webhook su Slack e Discord',
      ],
      image: '/img/screenshots/metrics.png',
      imageAlt: 'Grafico della durata delle esecuzioni nel tempo',
      imageWidth: 605,
      imageHeight: 375,
      imageSide: 'right',
    },
  ],

  apiBand: {
    text: 'Documentazione di API, riga di comando e webhook. Scegli da dove partire.',
    channels: [
      { icon: 'code', label: 'API REST', to: '/#api' },
      { icon: 'terminal', label: 'CLI', to: '/#api' },
      { icon: 'plug', label: 'Webhook', to: '/#api' },
    ],
  },

  testimonialsIntro: {
    title: 'Chi lo usa',
    lead:
      'Team che hanno smesso di tenere in piedi una macchina solo per farle girare '
      + 'sopra qualche riga di crontab.',
  },

  testimonials: [
    {
      name: 'Giulia Tomassini',
      role: 'Backend lead',
      quote:
        'Avevamo tre server con tre crontab diversi e nessuno sapeva più quale fosse '
        + 'quello buono. Ora la verità sta nel repository e la rivediamo in pull request.',
      avatar: '/img/people/1.svg',
      placeholder: true,
    },
    {
      name: 'Marco Renzi',
      role: 'Fondatore',
      quote:
        'Il job di fatturazione notturno era saltato per due settimane senza che ce ne '
        + 'accorgessimo. Adesso arriva un avviso al secondo tentativo fallito.',
      avatar: '/img/people/2.svg',
      placeholder: true,
    },
    {
      name: 'Sara Lombardi',
      role: 'Platform engineer',
      quote:
        'La modalità a intervallo ci ha tolto un servizio intero: quello che scriveva '
        + 'ogni dieci secondi su una coda lo fa Postqron.',
      avatar: '/img/people/3.svg',
      placeholder: true,
    },
    {
      name: 'Andrea Cesaroni',
      role: 'CTO',
      quote:
        'Staging e produzione hanno schedulazioni diverse e finalmente non è più una '
        + 'variabile d\'ambiente da ricordarsi a mano.',
      avatar: '/img/people/4.svg',
      placeholder: true,
    },
    {
      name: 'Davide Ferraro',
      role: 'Sviluppatore indipendente',
      quote:
        'Il piano gratuito copre tutti i miei progetti collaterali. Ci ho messo dieci '
        + 'minuti a spostarci sopra il primo.',
      avatar: '/img/people/5.svg',
      placeholder: true,
    },
    {
      name: 'Elena Nardi',
      role: 'Responsabile prodotto',
      quote:
        'I log delle esecuzioni li guarda anche chi non tocca il codice: è la prima '
        + 'cosa che apriamo quando un report non arriva.',
      avatar: '/img/people/6.svg',
      placeholder: true,
    },
  ],

  pricingIntro: {
    title: 'Piani',
    lead:
      'Si parte gratis e si cambia quando serve. I limiti sono applicati dal motore, '
      + 'non solo scritti qui.',
  },

  plans: [
    {
      name: 'Free',
      currency: '€',
      price: '0',
      period: '/mese',
      ctaLabel: 'Inizia gratis',
      ctaTo: '/#welcome',
      features: [
        { label: '20 cronjob', included: true },
        { label: 'Risoluzione di 1 minuto', included: true },
        { label: '3 giorni di log', included: true },
        { label: '1 repository cron.yaml', included: true },
        { label: 'Avvisi via email', included: true },
        { label: 'Staging e produzione', included: false },
        { label: 'Debug AI con la tua chiave', included: false },
        { label: 'Ruoli e permessi', included: false },
        { label: 'Workspace isolati e IP dedicato', included: false },
      ],
    },
    {
      name: 'Pro',
      currency: '€',
      price: '9',
      period: '/mese',
      annual: { price: '90', period: '/anno', savingNote: 'Due mesi in regalo' },
      featured: true,
      ctaLabel: 'Contattaci per Pro',
      ctaTo: '/contact',
      features: [
        { label: '200 cronjob', included: true },
        { label: 'Risoluzione di 10 secondi', included: true },
        { label: '15 giorni di log', included: true },
        { label: 'Repository illimitati', included: true },
        { label: 'Avvisi su Slack e Discord', included: true },
        { label: 'Staging e produzione', included: true },
        { label: 'Debug AI con la tua chiave', included: true },
        { label: 'Ruoli e permessi', included: false },
        { label: 'Workspace isolati e IP dedicato', included: false },
      ],
    },
    {
      name: 'Team',
      currency: '€',
      price: '29',
      period: '/mese',
      ctaLabel: 'Contattaci per Team',
      ctaTo: '/contact',
      features: [
        { label: 'Cronjob illimitati', included: true },
        { label: 'Risoluzione di 1 secondo', included: true },
        { label: '30 giorni di log, con export', included: true },
        { label: 'Repository illimitati', included: true },
        { label: 'Avvisi per membro e ambiente', included: true },
        { label: 'Staging e produzione', included: true },
        { label: 'Debug AI con la tua chiave', included: true },
        { label: 'Ruoli e permessi', included: true },
        { label: 'Workspace isolati e IP dedicato', included: false },
      ],
    },
    {
      name: 'Agency',
      pricePrefix: 'da',
      currency: '€',
      price: '79',
      period: '/mese',
      ctaLabel: 'Parliamone',
      ctaTo: '/contact',
      features: [
        { label: 'Cronjob illimitati', included: true },
        { label: 'Risoluzione di 1 secondo', included: true },
        { label: '90 giorni di log, con export', included: true },
        { label: 'Repository illimitati', included: true },
        { label: 'Avvisi per membro e ambiente', included: true },
        { label: 'Staging e produzione', included: true },
        { label: 'Debug AI con la tua chiave', included: true },
        { label: 'Ruoli e permessi', included: true },
        { label: 'Workspace isolati e IP dedicato', included: true },
      ],
    },
  ],

  stats: [
    { value: 1, label: 'Secondo di\nrisoluzione' },
    { value: 3, label: 'Tentativi\npredefiniti' },
    { value: 30, label: 'Secondi di\ntimeout' },
    { value: 90, label: 'Giorni di\nretention' },
  ],

  blogIntro: {
    title: 'Dal blog',
    lead: 'Note su schedulazione, affidabilità e sul mestiere di far partire le cose in orario.',
  },

  articles: [
    {
      title: 'Perché un cron su una sola macchina prima o poi ti tradisce',
      excerpt:
        'Riavvii, fusi orari e ora legale: i tre modi in cui una schedulazione che '
        + 'sembrava semplice smette di partire senza dirlo a nessuno.',
      image: '/img/blog/1.jpg',
      to: '/#blog',
    },
    {
      title: 'Cosa mettere in cron.yaml, e cosa lasciare nel codice',
      excerpt:
        'La schedulazione è configurazione, il lavoro no. Dove passa il confine e '
        + 'perché conviene tenerlo netto fin dal primo job.',
      image: '/img/blog/2.jpg',
      to: '/#blog',
    },
    {
      title: 'Ritentare bene: backoff, idempotenza e limiti sensati',
      excerpt:
        'Un tentativo in più può risolvere un guasto momentaneo o raddoppiare un '
        + 'addebito. Come scegliere la politica giusta per ogni job.',
      image: '/img/blog/3.jpg',
      to: '/#blog',
    },
  ],
}
