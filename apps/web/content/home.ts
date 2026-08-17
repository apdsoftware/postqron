import type {
  ApiChannel,
  Article,
  Feature,
  PricingPlan,
  Showcase,
  Stat,
  Testimonial,
} from '~/types/content'

/**
 * Contenuti della home.
 *
 * ⚠️ Testi **segnaposto**. Questa issue porta il design system e la struttura
 * della home; la redazione dei contenuti è la issue #402. In particolare le
 * testimonianze qui sotto sono fittizie e vanno sostituite con citazioni reali
 * (o rimosse) prima di pubblicare: pesi e ingombri servono a verificare il
 * layout, non a dichiarare clienti che non esistono.
 *
 * I dati dei piani, invece, sono quelli approvati in SPEC §8.
 */

export const hero = {
  title: 'Cronjob affidabili, definiti come codice',
  text:
    'Descrivi le schedulazioni in un file del tuo repository. PostQron le esegue '
    + 'all\'orario giusto, ritenta quando serve e ti dice sempre com\'è andata.',
  image: '/img/hero.jpg',
  imageAlt: 'La console di PostQron con l\'elenco delle esecuzioni',
  note: '30 giorni di prova sul piano Pro — nessuna carta di credito',
  /*
   * Segnaposto: il video di presentazione non è ancora stato girato e l'URL
   * arriva con #402. L'incorporamento usa il dominio senza cookie di YouTube e
   * parte solo alla pressione del pulsante — quando arriverà il banner cookie
   * (#405) questa fascia non avrà nulla da bloccare al caricamento.
   */
  video: {
    href: 'https://www.youtube.com/@postqron',
    embedSrc: 'https://www.youtube-nocookie.com/embed/videoseries?list=UU',
    title: 'Guarda PostQron in due minuti',
  },
} as const

export const featuresIntro = {
  title: 'Quattro cose che PostQron toglie dalla tua lista',
  lead:
    'Nessun server cron da tenere in piedi, nessuno script che fallisce in '
    + 'silenzio, nessun dubbio su cosa è stato eseguito e quando.',
} as const

export const features: readonly Feature[] = [
  {
    icon: 'checkSquare',
    title: 'Tutti i job in un posto',
    text: 'Ambienti e repository diversi, un elenco solo.',
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
]

export const showcases: readonly Showcase[] = [
  {
    title: 'Le schedulazioni vivono nel tuo repository',
    text:
      'Un file cron.yaml descrive job, orari e destinazioni. A ogni push PostQron '
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
]

export const apiBand = {
  text: 'Documentazione di API, riga di comando e webhook. Scegli da dove partire.',
  channels: [
    { icon: 'code', label: 'API REST', to: '/#api' },
    { icon: 'terminal', label: 'CLI', to: '/#api' },
    { icon: 'plug', label: 'Webhook', to: '/#api' },
  ] as readonly ApiChannel[],
} as const

export const testimonialsIntro = {
  title: 'Chi lo usa',
  lead:
    'Team che hanno smesso di tenere in piedi una macchina solo per farle girare '
    + 'sopra qualche riga di crontab.',
} as const

export const testimonials: readonly Testimonial[] = [
  {
    name: 'Giulia Tomassini',
    role: 'Backend lead',
    quote:
      'Avevamo tre server con crontab diversi e nessuno sapeva più quale fosse quello '
      + 'buono. Ora la verità sta nel repository e la rivediamo in pull request.',
    avatar: '/img/people/1.svg',
  },
  {
    name: 'Marco Renzi',
    role: 'Fondatore',
    quote:
      'Il job di fatturazione notturno era saltato per due settimane senza che ce ne '
      + 'accorgessimo. Adesso arriva un avviso al secondo tentativo fallito.',
    avatar: '/img/people/2.svg',
  },
  {
    name: 'Sara Lombardi',
    role: 'Platform engineer',
    quote:
      'La modalità a intervallo ci ha tolto un servizio intero: quello che scriveva '
      + 'ogni dieci secondi su una coda lo fa PostQron.',
    avatar: '/img/people/3.svg',
  },
  {
    name: 'Andrea Cesaroni',
    role: 'CTO',
    quote:
      'Staging e produzione hanno schedulazioni diverse e finalmente non è più una '
      + 'variabile d\'ambiente da ricordarsi a mano.',
    avatar: '/img/people/4.svg',
  },
  {
    name: 'Davide Ferraro',
    role: 'Sviluppatore indipendente',
    quote:
      'Il piano gratuito copre tutti i miei progetti collaterali. Ci ho messo dieci '
      + 'minuti a spostarci sopra il primo.',
    avatar: '/img/people/5.svg',
  },
  {
    name: 'Elena Nardi',
    role: 'Responsabile prodotto',
    quote:
      'I log delle esecuzioni li guarda anche chi non tocca il codice: è la prima '
      + 'cosa che apriamo quando un report non arriva.',
    avatar: '/img/people/6.svg',
  },
]

export const pricingIntro = {
  title: 'Piani',
  lead:
    'Si parte gratis e si cambia quando serve. I limiti sono applicati dal motore, '
    + 'non solo scritti qui.',
} as const

export const plans: readonly PricingPlan[] = [
  {
    name: 'Free',
    currency: '$',
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
    ],
  },
  {
    name: 'Pro',
    currency: '$',
    price: '12',
    period: '/mese',
    featured: true,
    ctaLabel: 'Prova 30 giorni',
    ctaTo: '/#welcome',
    features: [
      { label: '200 cronjob', included: true },
      { label: 'Risoluzione di 10 secondi', included: true },
      { label: '15 giorni di log', included: true },
      { label: 'Repository illimitati', included: true },
      { label: 'Avvisi su Slack e Discord', included: true },
      { label: 'Staging e produzione', included: true },
      { label: 'Debug AI con la tua chiave', included: true },
      { label: 'Ruoli e permessi', included: false },
    ],
  },
  {
    name: 'Team',
    currency: '$',
    price: '39',
    period: '/mese',
    ctaLabel: 'Scegli Team',
    ctaTo: '/#welcome',
    features: [
      { label: 'Cronjob illimitati', included: true },
      { label: 'Risoluzione di 1 secondo', included: true },
      { label: '30 giorni di log ed export', included: true },
      { label: 'Repository illimitati', included: true },
      { label: 'Avvisi per membro e ambiente', included: true },
      { label: 'Staging e produzione', included: true },
      { label: 'Debug AI con la tua chiave', included: true },
      { label: 'Ruoli e permessi', included: true },
    ],
  },
]

/**
 * Numeri della fascia statistiche: sono le costanti del prodotto dichiarate in
 * SPEC §8 e §9, non metriche di adozione.
 */
export const stats: readonly Stat[] = [
  { value: 1, label: 'Secondo di\nrisoluzione' },
  { value: 3, label: 'Tentativi\npredefiniti' },
  { value: 30, label: 'Secondi di\ntimeout' },
  { value: 90, label: 'Giorni di\nretention' },
]

export const blogIntro = {
  title: 'Dal blog',
  lead: 'Note su schedulazione, affidabilità e sul mestiere di far partire le cose in orario.',
} as const

export const articles: readonly Article[] = [
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
]
