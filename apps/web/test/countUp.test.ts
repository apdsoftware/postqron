import { describe, expect, it } from 'vitest'

import { countUpValue, formatCount } from '~/utils/countUp'

describe('countUpValue', () => {
  it('parte da zero e arriva esattamente al valore', () => {
    expect(countUpValue(202, 0)).toBe(0)
    expect(countUpValue(202, 1)).toBe(202)
  })

  it('rallenta verso la fine invece di crescere in modo lineare', () => {
    // A metà corsa un ease-out cubico ha già coperto sette ottavi del percorso.
    expect(countUpValue(800, 0.5)).toBe(700)
  })

  it('non supera il valore né scende sotto zero con un progresso fuori scala', () => {
    expect(countUpValue(90, 1.4)).toBe(90)
    expect(countUpValue(90, -0.2)).toBe(0)
  })

  it('cresce in modo monotono', () => {
    let previous = -1
    for (let progress = 0; progress <= 1; progress += 0.05) {
      const value = countUpValue(1000, progress)
      expect(value).toBeGreaterThanOrEqual(previous)
      previous = value
    }
  })
})

describe('formatCount', () => {
  it('lascia intatti i numeri sotto il migliaio', () => {
    expect(formatCount(0)).toBe('0')
    expect(formatCount(999)).toBe('999')
  })

  it('raggruppa le migliaia con il punto', () => {
    expect(formatCount(1000)).toBe('1.000')
    expect(formatCount(1234567)).toBe('1.234.567')
  })

  it('conserva il segno', () => {
    expect(formatCount(-4200)).toBe('-4.200')
  })
})
