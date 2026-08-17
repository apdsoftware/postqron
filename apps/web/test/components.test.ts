import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import HexIcon from '~/components/ui/HexIcon.vue'
import HexagonAvatar from '~/components/ui/HexagonAvatar.vue'
import HexagonShape from '~/components/ui/HexagonShape.vue'
import LineButton from '~/components/ui/LineButton.vue'
import PricingCard from '~/components/ui/PricingCard.vue'
import TestimonialCard from '~/components/ui/TestimonialCard.vue'
import { hexIcons } from '~/utils/icons'
import type { PricingPlan } from '~/types/content'

describe('HexIcon', () => {
  it('disegna il tracciato del glifo richiesto', () => {
    const wrapper = mount(HexIcon, { props: { name: 'play' } })
    expect(wrapper.find('path').attributes('d')).toBe(hexIcons.play.path)
  })

  it('dimensiona in em secondo la larghezza di avanzamento del glifo', () => {
    const wrapper = mount(HexIcon, { props: { name: 'angleRight' } })
    // 640/1792 em: è la larghezza che il glifo occupava nel webfont.
    // Il confronto è numerico perché il browser arrotonda il valore in stile.
    expect(Number.parseFloat(wrapper.element.style.width)).toBeCloseTo(640 / 1792, 5)
    expect(wrapper.element.style.width).toMatch(/em$/)
    expect(wrapper.attributes('viewBox')).toBe('0 0 640 1792')
  })

  it('è decorativa quando non ha etichetta', () => {
    const wrapper = mount(HexIcon, { props: { name: 'code' } })
    expect(wrapper.attributes('aria-hidden')).toBe('true')
    expect(wrapper.find('title').exists()).toBe(false)
  })

  it('diventa un\'immagine con nome accessibile quando l\'etichetta c\'è', () => {
    const wrapper = mount(HexIcon, { props: { name: 'github', label: 'GitHub' } })
    expect(wrapper.attributes('aria-hidden')).toBeUndefined()
    expect(wrapper.attributes('role')).toBe('img')
    expect(wrapper.find('title').text()).toBe('GitHub')
  })
})

describe('HexagonAvatar', () => {
  it('propaga la larghezza richiesta alla variabile della forma', () => {
    const wrapper = mount(HexagonAvatar, { props: { src: '/a.svg', alt: 'Foto', size: 120 } })
    expect(wrapper.attributes('style')).toContain('--avatar-width: 120px')
  })

  it('mantiene testo alternativo e caricamento pigro sull\'immagine', () => {
    const wrapper = mount(HexagonAvatar, { props: { src: '/a.svg', alt: 'Foto di Giulia' } })
    const image = wrapper.find('img')
    expect(image.attributes('alt')).toBe('Foto di Giulia')
    expect(image.attributes('loading')).toBe('lazy')
  })
})

describe('HexagonShape', () => {
  it('ricava dalle misure del tema la quota delle punte', () => {
    // 60×67 con punte da 15: 15/67 dell'altezza per lato.
    const style = mount(HexagonShape).attributes('style')
    expect(style).toContain('--hex-width: 60px')
    expect(style).toContain('--hex-height: 67px')
    expect(style).toContain(`--hex-cap: ${(15 / 67) * 100}%`)
  })

  it('resta un esagono regolare quando le punte valgono un quarto', () => {
    const style = mount(HexagonShape, { props: { width: 48, body: 28, cap: 14 } }).attributes('style')
    expect(style).toContain('--hex-height: 56px')
    expect(style).toContain('--hex-cap: 25%')
  })
})

const testimonial = {
  name: 'Giulia Tomassini',
  role: 'Backend lead',
  quote: 'Ora la verità sta nel repository.',
  avatar: '/img/people/1.svg',
  photoAlt: 'Foto di Giulia Tomassini',
  placeholder: true,
}

describe('TestimonialCard', () => {
  it('cita nome, ruolo e testo dentro un blockquote', () => {
    const wrapper = mount(TestimonialCard, { props: testimonial })

    expect(wrapper.find('blockquote cite').text()).toBe('Giulia Tomassini')
    expect(wrapper.text()).toContain('Backend lead')
    expect(wrapper.find('blockquote p').text()).toBe('Ora la verità sta nel repository.')
    expect(wrapper.find('img').attributes('alt')).toBe('Foto di Giulia Tomassini')
  })

  it('marca nel markup le citazioni inventate, e solo quelle', () => {
    // È il punto in cui la finzione smette di essere un commento nel file dei
    // contenuti e diventa qualcosa che il percorso di deploy (#426) può vedere.
    const invented = mount(TestimonialCard, { props: testimonial })
    expect(invented.attributes('data-placeholder')).toBe('true')

    const real = mount(TestimonialCard, { props: { ...testimonial, placeholder: false } })
    expect(real.attributes('data-placeholder')).toBeUndefined()
  })
})

const plan: PricingPlan = {
  name: 'Pro',
  currency: '€',
  price: '9',
  period: '/mese',
  ctaLabel: 'Scegli Pro',
  ctaTo: '/#welcome',
  features: [
    { label: '200 cronjob', included: true },
    { label: 'Ruoli e permessi', included: false },
  ],
}

const href = '/it/#welcome'

/** Le due convenzioni locali che agiscono sulla riga del prezzo. */
const italian = { currencyPosition: 'after', taxNote: '+ IVA' } as const
const english = { currencyPosition: 'before', taxNote: '+ VAT' } as const

describe('PricingCard', () => {
  it('distingue le voci comprese da quelle escluse', () => {
    const wrapper = mount(PricingCard, { props: { plan, position: 2, href, ...italian } })
    const items = wrapper.findAll('li')

    expect(items).toHaveLength(2)
    expect(items[0]!.classes()).toContain('is-included')
    expect(items[1]!.classes()).not.toContain('is-included')
  })

  it('mostra prezzo, periodo e posizione nel listino', () => {
    const wrapper = mount(PricingCard, { props: { plan, position: 2, href, ...italian } })

    expect(wrapper.text()).toContain('\u20ac')
    expect(wrapper.text()).toContain('9')
    expect(wrapper.text()).toContain('/mese')
    expect(wrapper.find('.pricing__position').text()).toBe('2')
    expect(wrapper.find('.pricing__prefix').exists()).toBe(false)
  })

  it('antepone il qualificatore ai piani che partono da una soglia', () => {
    // SPEC §8 dichiara Agency «da €79/mese»: un `€79` secco prometterebbe un
    // prezzo fisso che non esiste.
    const wrapper = mount(PricingCard, {
      props: { plan: { ...plan, pricePrefix: 'da', price: '79' }, position: 4, href, ...italian },
    })

    // Lo spazio dopo il qualificatore è nel testo e non solo nel margine: un
    // margine non finisce negli appunti di chi copia il prezzo.
    expect(wrapper.find('.pricing__prefix').element.textContent).toBe('da\u00a0')
    expect(wrapper.find('.pricing__price').text()).toContain('da\u00a079')
  })

  it('allinea il qualificatore a ciò che lo segue', () => {
    // Con il simbolo in coda il qualificatore precede la cifra grande: se
    // restasse sollevato sembrerebbe il richiamo a una nota.
    const posposto = mount(PricingCard, {
      props: { plan: { ...plan, pricePrefix: 'da' }, position: 4, href, ...italian },
    })
    expect(posposto.find('.pricing__prefix').classes()).toContain('pricing__prefix--baseline')

    const anteposto = mount(PricingCard, {
      props: { plan: { ...plan, pricePrefix: 'from' }, position: 4, href, ...english },
    })
    expect(anteposto.find('.pricing__prefix').classes()).not.toContain('pricing__prefix--baseline')
  })

  it('scrive il prezzo secondo la convenzione della lingua', () => {
    // Due regole locali sulla stessa riga: dove va il simbolo e come si chiama
    // l'imposta. La valuta e l'importo, invece, non cambiano mai (R61).
    const inEnglish = mount(PricingCard, { props: { plan, position: 2, href, ...english } })
    const inItalian = mount(PricingCard, { props: { plan, position: 2, href, ...italian } })

    expect(inEnglish.find('.pricing__price').text()).toContain('\u20ac9')
    expect(inItalian.find('.pricing__price').text()).toContain('9\u00a0\u20ac')
  })

  it('separa cifra e simbolo posposto con uno spazio unificatore', () => {
    // «9 €» è una quantità sola: spezzarla a fine riga la renderebbe due cose.
    // Il confronto è su `textContent` e non su `text()`, che toglie gli spazi
    // ai bordi — compreso quello che il test deve vedere.
    const wrapper = mount(PricingCard, { props: { plan, position: 2, href, ...italian } })

    expect(wrapper.find('.pricing__currency--after').element.textContent).toBe('\u00a0\u20ac')
  })

  it('dichiara l\'imposta accanto al prezzo, nella lingua della pagina', () => {
    // R61-bis: gli importi sono al netto e una cifra senza indicazione è un
    // difetto. La dicitura arriva tradotta, non è un suffisso del componente.
    const inGerman = mount(PricingCard, {
      props: { plan, position: 2, href, currencyPosition: 'after', taxNote: '+ MwSt.' },
    })

    expect(inGerman.find('.pricing__tax').text()).toBe('+ MwSt.')

    // Sta fuori dal paragrafo del prezzo: è un'affermazione distinta sulla
    // cifra, e da lì il testo selezionato non esce come «/Monat+ MwSt.».
    expect(inGerman.find('.pricing__price').text()).not.toContain('MwSt.')
  })

  it('manda il pulsante alla destinazione già tradotta in lingua', () => {
    const wrapper = mount(PricingCard, { props: { plan, position: 2, href, ...italian } })

    // La destinazione arriva prefissata dalla pagina: la card non conosce la
    // lingua, e `plan.ctaTo` da solo punterebbe fuori da tutte e cinque.
    expect(wrapper.findComponent(LineButton).props('to')).toBe(href)
  })

  it('evidenzia il piano in vetrina e ne rende pieno il pulsante', () => {
    const plain = mount(PricingCard, { props: { plan, position: 1, href, ...italian } })
    expect(plain.classes()).not.toContain('is-featured')
    expect(plain.find('.line-button').classes()).toContain('line-button--outline')

    const featured = mount(PricingCard, {
      props: { plan: { ...plan, featured: true }, position: 2, href, ...italian },
    })
    expect(featured.classes()).toContain('is-featured')
    expect(featured.find('.line-button').classes()).toContain('line-button--solid')
  })
})
