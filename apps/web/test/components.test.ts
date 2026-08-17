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
  currency: '$',
  price: '12',
  period: '/mese',
  ctaLabel: 'Prova 30 giorni',
  ctaTo: '/#welcome',
  features: [
    { label: '200 cronjob', included: true },
    { label: 'Ruoli e permessi', included: false },
  ],
}

const href = '/it/#welcome'

describe('PricingCard', () => {
  it('distingue le voci comprese da quelle escluse', () => {
    const wrapper = mount(PricingCard, { props: { plan, position: 2, href } })
    const items = wrapper.findAll('li')

    expect(items).toHaveLength(2)
    expect(items[0]!.classes()).toContain('is-included')
    expect(items[1]!.classes()).not.toContain('is-included')
  })

  it('mostra prezzo, periodo e posizione nel listino', () => {
    const wrapper = mount(PricingCard, { props: { plan, position: 2, href } })

    expect(wrapper.text()).toContain('$')
    expect(wrapper.text()).toContain('12')
    expect(wrapper.text()).toContain('/mese')
    expect(wrapper.find('.pricing__position').text()).toBe('2')
    expect(wrapper.find('.pricing__prefix').exists()).toBe(false)
  })

  it('antepone il qualificatore ai piani che partono da una soglia', () => {
    // SPEC §8 dichiara Agency «da $99/mese»: un `$99` secco prometterebbe un
    // prezzo fisso che non esiste.
    const wrapper = mount(PricingCard, {
      props: { plan: { ...plan, pricePrefix: 'da', price: '99' }, position: 4, href },
    })

    expect(wrapper.find('.pricing__prefix').text()).toBe('da')
  })

  it('manda il pulsante alla destinazione già tradotta in lingua', () => {
    const wrapper = mount(PricingCard, { props: { plan, position: 2, href } })

    // La destinazione arriva prefissata dalla pagina: la card non conosce la
    // lingua, e `plan.ctaTo` da solo punterebbe fuori da tutte e cinque.
    expect(wrapper.findComponent(LineButton).props('to')).toBe(href)
  })

  it('evidenzia il piano in vetrina e ne rende pieno il pulsante', () => {
    const plain = mount(PricingCard, { props: { plan, position: 1, href } })
    expect(plain.classes()).not.toContain('is-featured')
    expect(plain.find('.line-button').classes()).toContain('line-button--outline')

    const featured = mount(PricingCard, {
      props: { plan: { ...plan, featured: true }, position: 2, href },
    })
    expect(featured.classes()).toContain('is-featured')
    expect(featured.find('.line-button').classes()).toContain('line-button--solid')
  })
})
