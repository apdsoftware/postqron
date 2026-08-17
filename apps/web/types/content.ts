import type { HexIconName } from '~/utils/icons'

/**
 * Forma dei contenuti del sito pubblico.
 *
 * I componenti non conoscono i testi: ricevono questi oggetti. Le issue di
 * contenuto (#402 pagine, #403 prezzi, #404 legali) lavorano sui dati in
 * `content/`, non sul markup.
 */

export interface NavItem {
  label: string
  /** Destinazione: ancora della home (`#prezzi`) o percorso (`/prezzi`). */
  to?: string
  /** Voci di secondo livello: rendono la voce un menu a tendina. */
  children?: readonly NavItem[]
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
}

export interface PlanFeature {
  label: string
  /** Voce compresa nel piano: le altre restano visibili ma spente. */
  included: boolean
}

export interface PricingPlan {
  name: string
  currency: string
  price: string
  period: string
  features: readonly PlanFeature[]
  ctaLabel: string
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
  to: string
}

export interface ApiChannel {
  icon: HexIconName
  label: string
  to: string
}
