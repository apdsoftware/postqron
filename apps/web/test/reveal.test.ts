import { describe, expect, it } from 'vitest'

import { revealStyle, revealStyleAttribute } from '~/utils/reveal'

describe('revealStyle', () => {
  it('applica i valori predefiniti del tema', () => {
    expect(revealStyle()).toEqual({
      '--pq-reveal-duration': '0.6s',
      '--pq-reveal-delay': '0s',
      '--pq-reveal-y': '50px',
    })
  })

  it('sposta lungo Y per le direzioni verticali, con il segno giusto', () => {
    expect(revealStyle({ direction: 'bottom', distance: '30px' })['--pq-reveal-y']).toBe('30px')
    expect(revealStyle({ direction: 'top', distance: '30px' })['--pq-reveal-y']).toBe('-30px')
  })

  it('sposta lungo X per le direzioni orizzontali, e non lungo Y', () => {
    const fromRight = revealStyle({ direction: 'right', distance: '30px' })
    expect(fromRight['--pq-reveal-x']).toBe('30px')
    expect(fromRight['--pq-reveal-y']).toBeUndefined()

    expect(revealStyle({ direction: 'left', distance: '30px' })['--pq-reveal-x']).toBe('-30px')
  })

  it('riporta durata e ritardo in secondi', () => {
    const style = revealStyle({ duration: 1.2, delay: 0.4 })
    expect(style['--pq-reveal-duration']).toBe('1.2s')
    expect(style['--pq-reveal-delay']).toBe('0.4s')
  })
})

describe('revealStyleAttribute', () => {
  it('produce un attributo style utilizzabile lato server', () => {
    expect(revealStyleAttribute({ direction: 'bottom', distance: '50px', delay: 0.2 })).toBe(
      '--pq-reveal-duration:0.6s;--pq-reveal-delay:0.2s;--pq-reveal-y:50px',
    )
  })
})
