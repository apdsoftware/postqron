import type { SiteContent } from '~/types/content'

/**
 * Contenuti del sito pubblico in **inglese**, lingua sorgente (SPEC §8-bis).
 *
 * Le altre quattro lingue traducono da qui: si scrive in inglese e si traduce,
 * non il contrario. Chi cambia un testo commerciale lo cambia prima in questo
 * file, poi negli altri.
 *
 * ⚠️ Restano segnaposto, dichiarati dove stanno: le testimonianze
 * (`placeholder: true`), l'URL del video di presentazione e l'indirizzo legale,
 * che arriva con i valori di SPEC §7 Q3. I dati dei piani, invece, sono quelli
 * approvati in SPEC §8, Agency compreso.
 */
export const en: SiteContent = {
  meta: {
    title: 'Reliable cron jobs, defined as code',
    description:
      'Describe your schedules in a file in your repository. Postqron runs them on '
      + 'time, retries when it should, and always tells you how it went.',
  },

  ui: {
    menu: 'Menu',
    homeLink: 'Postqron, back to the home page',
    language: 'Language',
    emailPlaceholder: 'Enter your email',
    emailSubmit: 'Get started',
    readMore: 'Read',
    closeVideo: 'Close',
    photoOf: 'Photo of {name}',
    contactTitle: 'Contact',
    emailPrefix: 'Email: ',
    rightsReserved: 'All rights reserved.',
  },

  legal: {
    sourceNotice: 'This document is not yet available in your language. The English original is shown below.',
    versionLabel: 'Version',
    effectiveDateLabel: 'Effective date',
  },

  nav: {
    main: [
      { label: 'Home', to: '/#welcome' },
      {
        label: 'Product',
        children: [
          { label: 'Features', to: '/#features' },
          { label: 'Testimonials', to: '/#testimonials' },
          { label: 'Pricing', to: '/#pricing' },
        ],
      },
      {
        label: 'Resources',
        children: [
          { label: 'API and webhooks', to: '/#api' },
          { label: 'From the blog', to: '/#blog' },
        ],
      },
      { label: 'Contact', to: '/#contact' },
    ],
    cta: { label: 'Try for free', to: '/#welcome' },
    footer: [
      {
        title: 'Product',
        items: [
          { label: 'Features', to: '/#features' },
          { label: 'Pricing', to: '/#pricing' },
          { label: 'API and webhooks', to: '/#api' },
          { label: 'Blog', to: '/#blog' },
        ],
      },
      {
        title: 'Support',
        items: [
          { label: 'Testimonials', to: '/#testimonials' },
          { label: 'Contact', to: '/#contact' },
        ],
      },
      {
        title: 'Legal',
        items: [
          { label: 'Terms of Service', to: '/legal/terms-of-service' },
          { label: 'Privacy Policy', to: '/legal/privacy-policy' },
          { label: 'Cookie Policy', to: '/legal/cookie-policy' },
          { label: 'Acceptable Use', to: '/legal/acceptable-use-policy' },
        ],
      },
    ],
  },

  company: {
    name: 'Postqron',
    legalName: 'Postqron',
    about:
      'Managed cron jobs, defined in your own repository and brought back to a '
      + 'single place: scheduling you can rely on, a log of every run, and an alert '
      + 'when something does not start.',
    address: 'Address to be confirmed',
    email: 'support@postqron.com',
  },

  // In inglese il simbolo precede la cifra e non ha spazio: «€9 + VAT».
  money: { currencyPosition: 'before', taxNote: '+ VAT' },

  hero: {
    title: 'Reliable cron jobs, defined as code',
    text:
      'Describe your schedules in a file in your repository. Postqron runs them on '
      + 'time, retries when it should, and always tells you how it went.',
    image: '/img/hero.jpg',
    imageAlt: 'The Postqron console showing the list of runs',
  },

  featuresIntro: {
    title: 'Four things Postqron takes off your list',
    lead:
      'No cron server to keep alive, no script failing in silence, no doubt about '
      + 'what ran and when.',
  },

  features: [
    {
      icon: 'checkSquare',
      title: 'Every job in one place',
      text: 'Different repositories and environments, a single list.',
    },
    {
      icon: 'github',
      title: 'Defined in your repository',
      text: 'One cron.yaml, read again on every push to the branch.',
      highlighted: true,
    },
    {
      icon: 'barChart',
      title: 'A log of every run',
      text: 'Duration, outcome and response, as they happen.',
    },
    {
      icon: 'bell',
      title: 'Retries and alerts',
      text: 'Backoff on failure, a notice when that is not enough.',
    },
  ],

  showcases: [
    {
      title: 'Your schedules live in your repository',
      text:
        'A cron.yaml file describes jobs, times and targets. On every push Postqron '
        + 'reads it again and realigns everything: review happens in the pull request.',
      bullets: [
        'Cron expressions with time zones, daylight saving included',
        'Interval mode down to the second',
        'Separate environments for staging and production',
        'Syntax errors reported on the commit',
      ],
      image: '/img/screenshots/jobs.png',
      imageAlt: 'List of cron jobs synced from a repository',
      imageWidth: 593,
      imageHeight: 467,
      imageSide: 'left',
    },
    {
      title: 'You always know how it went',
      text:
        'Every occurrence leaves a trace: when it started, how long it took, what it '
        + 'answered. Failures become a notice, not a discovery.',
      bullets: [
        'Run history with filters by outcome',
        'Average duration and failure rate per job',
        'Alerts by email or webhook on Slack and Discord',
      ],
      image: '/img/screenshots/metrics.png',
      imageAlt: 'Chart of run duration over time',
      imageWidth: 605,
      imageHeight: 375,
      imageSide: 'right',
    },
  ],

  apiBand: {
    text: 'Docs for the API, the command line and webhooks. Pick where to start.',
    channels: [
      { icon: 'code', label: 'REST API', to: '/#api' },
      { icon: 'terminal', label: 'CLI', to: '/#api' },
      { icon: 'plug', label: 'Webhooks', to: '/#api' },
    ],
  },

  testimonialsIntro: {
    title: 'Who uses it',
    lead:
      'Teams that stopped keeping a machine alive just to run a few lines of crontab '
      + 'on top of it.',
  },

  testimonials: [
    {
      name: 'Giulia Tomassini',
      role: 'Backend lead',
      quote:
        'We had three servers with three different crontabs and nobody knew which one '
        + 'was the real one. Now the truth is in the repository and we review it in a '
        + 'pull request.',
      avatar: '/img/people/1.svg',
      placeholder: true,
    },
    {
      name: 'Marco Renzi',
      role: 'Founder',
      quote:
        'The nightly billing job had been failing for two weeks and none of us had '
        + 'noticed. Now an alert arrives on the second failed attempt.',
      avatar: '/img/people/2.svg',
      placeholder: true,
    },
    {
      name: 'Sara Lombardi',
      role: 'Platform engineer',
      quote:
        'Interval mode removed an entire service for us: what used to write to a queue '
        + 'every ten seconds is now Postqron.',
      avatar: '/img/people/3.svg',
      placeholder: true,
    },
    {
      name: 'Andrea Cesaroni',
      role: 'CTO',
      quote:
        'Staging and production run different schedules, and at last that is no longer '
        + 'an environment variable somebody has to remember by hand.',
      avatar: '/img/people/4.svg',
      placeholder: true,
    },
    {
      name: 'Davide Ferraro',
      role: 'Independent developer',
      quote:
        'The free plan covers every one of my side projects. Moving the first one over '
        + 'took me ten minutes.',
      avatar: '/img/people/5.svg',
      placeholder: true,
    },
    {
      name: 'Elena Nardi',
      role: 'Head of product',
      quote:
        'People who never touch the code read the run logs too: it is the first thing '
        + 'we open when a report does not show up.',
      avatar: '/img/people/6.svg',
      placeholder: true,
    },
  ],

  pricingIntro: {
    title: 'Plans',
    lead:
      'Start free and move up when you need to. The limits are enforced by the engine, '
      + 'not just written here.',
  },

  plans: [
    {
      name: 'Free',
      currency: '€',
      price: '0',
      period: '/month',
      ctaLabel: 'Start free',
      ctaTo: '/#welcome',
      features: [
        { label: '20 cron jobs', included: true },
        { label: '1-minute resolution', included: true },
        { label: '3 days of logs', included: true },
        { label: '1 cron.yaml repository', included: true },
        { label: 'Email alerts', included: true },
        { label: 'Staging and production', included: false },
        { label: 'AI debugging with your key', included: false },
        { label: 'Roles and permissions', included: false },
        { label: 'Isolated workspaces and dedicated IP', included: false },
      ],
    },
    {
      name: 'Pro',
      currency: '€',
      price: '9',
      period: '/month',
      featured: true,
      ctaLabel: 'Choose Pro',
      ctaTo: '/#welcome',
      features: [
        { label: '200 cron jobs', included: true },
        { label: '10-second resolution', included: true },
        { label: '15 days of logs', included: true },
        { label: 'Unlimited repositories', included: true },
        { label: 'Slack and Discord alerts', included: true },
        { label: 'Staging and production', included: true },
        { label: 'AI debugging with your key', included: true },
        { label: 'Roles and permissions', included: false },
        { label: 'Isolated workspaces and dedicated IP', included: false },
      ],
    },
    {
      name: 'Team',
      currency: '€',
      price: '29',
      period: '/month',
      ctaLabel: 'Choose Team',
      ctaTo: '/#welcome',
      features: [
        { label: 'Unlimited cron jobs', included: true },
        { label: '1-second resolution', included: true },
        { label: '30 days of logs, with export', included: true },
        { label: 'Unlimited repositories', included: true },
        { label: 'Alerts per member and environment', included: true },
        { label: 'Staging and production', included: true },
        { label: 'AI debugging with your key', included: true },
        { label: 'Roles and permissions', included: true },
        { label: 'Isolated workspaces and dedicated IP', included: false },
      ],
    },
    {
      name: 'Agency',
      pricePrefix: 'from',
      currency: '€',
      price: '79',
      period: '/month',
      ctaLabel: 'Talk to us',
      ctaTo: '/#contact',
      features: [
        { label: 'Unlimited cron jobs', included: true },
        { label: '1-second resolution', included: true },
        { label: '90 days of logs, with export', included: true },
        { label: 'Unlimited repositories', included: true },
        { label: 'Alerts per member and environment', included: true },
        { label: 'Staging and production', included: true },
        { label: 'AI debugging with your key', included: true },
        { label: 'Roles and permissions', included: true },
        { label: 'Isolated workspaces and dedicated IP', included: true },
      ],
    },
  ],

  stats: [
    { value: 1, label: 'Second of\nresolution' },
    { value: 3, label: 'Attempts by\ndefault' },
    { value: 30, label: 'Seconds of\ntimeout' },
    { value: 90, label: 'Days of\nretention' },
  ],

  blogIntro: {
    title: 'From the blog',
    lead: 'Notes on scheduling, reliability and the craft of starting things on time.',
  },

  articles: [
    {
      title: 'Why a cron on a single machine will let you down sooner or later',
      excerpt:
        'Reboots, time zones and daylight saving: the three ways a schedule that looked '
        + 'simple stops running without telling anyone.',
      image: '/img/blog/1.jpg',
      to: '/#blog',
    },
    {
      title: 'What belongs in cron.yaml, and what belongs in your code',
      excerpt:
        'Scheduling is configuration; the work is not. Where the line falls, and why it '
        + 'pays to keep it sharp from the very first job.',
      image: '/img/blog/2.jpg',
      to: '/#blog',
    },
    {
      title: 'Retrying well: backoff, idempotency and sensible limits',
      excerpt:
        'One more attempt can fix a passing glitch or charge a customer twice. How to '
        + 'pick the right policy for each job.',
      image: '/img/blog/3.jpg',
      to: '/#blog',
    },
  ],
}
