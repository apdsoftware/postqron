import type { HexIconName } from '~/utils/icons'

/**
 * Forma dei contenuti del sito pubblico.
 *
 * I componenti non conoscono i testi: ricevono questi oggetti. Un componente
 * che contiene una frase è un difetto, perché non è traducibile (SPEC §8-bis);
 * `test/no-strings.test.ts` verifica che non ne rientri nessuna.
 *
 * Ogni lingua compila `SiteContent` per intero in `content/<codice>.ts`.
 * L'inglese è la lingua sorgente: si scrive lì e si traduce da lì.
 */

export interface NavItem {
  label: string
  /**
   * Destinazione **senza prefisso di lingua**: `/`, `/#pricing`, `/pricing`.
   * La stessa voce vale per tutte e cinque le lingue, ed è `localePath()` a
   * darle il prefisso al momento del rendering.
   */
  to?: string
  /** Voci di secondo livello: rendono la voce un menu a tendina. */
  children?: readonly NavItem[]
}

export interface NavGroup {
  title: string
  items: readonly NavItem[]
}

export interface Feature {
  icon: HexIconName
  title: string
  text: string
  to?: string
  /** Una sola card per griglia è evidenziata, come nel tema. */
  highlighted?: boolean
}

export interface Showcase {
  title: string
  text: string
  bullets: readonly string[]
  image: string
  imageAlt: string
  imageWidth: number
  imageHeight: number
  imageSide: 'left' | 'right'
}

export interface Testimonial {
  name: string
  role: string
  quote: string
  avatar: string
  /**
   * Contenuto **inventato**, da sostituire con una citazione reale o da
   * rimuovere prima della pubblicazione.
   *
   * È un campo obbligatorio e non un commento in testa al file perché una
   * testimonianza fittizia che finisce online non è un refuso di redazione: qui
   * la finzione è un dato, interrogabile da chi possiede il percorso di deploy
   * (#426) e reso nel markup come `data-placeholder`. Questa issue non lo
   * trasforma in un errore di build: quella decisione sta a valle.
   */
  placeholder: boolean
}

export interface PlanFeature {
  label: string
  /** Voce compresa nel piano: le altre restano visibili ma spente. */
  included: boolean
}

export interface PricingPlan {
  name: string
  /**
   * Qualificatore davanti al prezzo, per i piani che partono da una soglia
   * invece di costare una cifra fissa: «from», «da». SPEC §8 dichiara Agency
   * «da $99/mese», e un `$99` secco prometterebbe un prezzo che non è quello.
   */
  pricePrefix?: string
  currency: string
  price: string
  period: string
  features: readonly PlanFeature[]
  ctaLabel: string
  /** Destinazione senza prefisso di lingua, come in `NavItem`. */
  ctaTo: string
  /** Il piano in evidenza, con intestazione a gradiente. */
  featured?: boolean
}

export interface Stat {
  value: number
  /** `\n` manda a capo, come il `<br>` del tema. */
  label: string
}

export interface Article {
  title: string
  excerpt: string
  image: string
  /** Destinazione senza prefisso di lingua, come in `NavItem`. */
  to: string
}

export interface ApiChannel {
  icon: HexIconName
  label: string
  /** Destinazione senza prefisso di lingua, come in `NavItem`. */
  to: string
}

export interface SocialLink {
  label: string
  href: string
  icon: HexIconName
}

export interface Intro {
  title: string
  lead: string
}

export interface Hero {
  title: string
  text: string
  image: string
  imageAlt: string
  /** Nota sotto al campo email. */
  note: string
  video: {
    href: string
    embedSrc: string
    title: string
  }
}

export interface ApiBand {
  text: string
  channels: readonly ApiChannel[]
}

export interface CompanyInfo {
  name: string
  legalName: string
  about: string
  address: string
  email: string
}

/**
 * Etichette dell'interfaccia: tutto ciò che il tema scriveva dentro il markup e
 * che qui non può starci. Sono poche e devono restare tali — una frase di
 * prodotto appartiene alle sezioni sotto, non a questo elenco.
 */
export interface UiLabels {
  /** Nome accessibile del pulsante a panino. */
  menu: string
  /** Nome accessibile del logo, che riporta alla home. */
  homeLink: string
  /** Nome accessibile del selettore di lingua. */
  language: string
  emailPlaceholder: string
  emailSubmit: string
  /** Invito in fondo alle anteprime degli articoli. */
  readMore: string
  /** Pulsante di chiusura della finestra del video. */
  closeVideo: string
  /** Testo alternativo del ritratto di una testimonianza, con `{name}`. */
  photoOf: string
  /** Intestazione della colonna dei recapiti nel piè di pagina. */
  contactTitle: string
  /** Etichetta davanti all'indirizzo email, spazio finale compreso. */
  emailPrefix: string
  /** Formula di copyright dopo l'anno e la ragione sociale. */
  rightsReserved: string
}

export interface SiteContent {
  meta: {
    /** Titolo della home, prima del suffisso col nome del prodotto. */
    title: string
    description: string
  }
  ui: UiLabels
  nav: {
    main: readonly NavItem[]
    cta: Required<Pick<NavItem, 'label' | 'to'>>
    footer: readonly NavGroup[]
  }
  company: CompanyInfo
  hero: Hero
  featuresIntro: Intro
  features: readonly Feature[]
  showcases: readonly Showcase[]
  apiBand: ApiBand
  testimonialsIntro: Intro
  testimonials: readonly Testimonial[]
  pricingIntro: Intro
  plans: readonly PricingPlan[]
  stats: readonly Stat[]
  blogIntro: Intro
  articles: readonly Article[]
}
