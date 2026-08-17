import { describe, expect, it } from 'vitest'

import { apiUrl, buildQuery } from '../utils/api'

describe('apiUrl', () => {
  it('unisce base URL e percorso', () => {
    expect(apiUrl('/v1/jobs', 'https://api.postqron.com')).toBe('https://api.postqron.com/v1/jobs')
  })

  it('non duplica gli slash', () => {
    expect(apiUrl('/v1/jobs', 'https://api.postqron.com/')).toBe('https://api.postqron.com/v1/jobs')
    expect(apiUrl('v1/jobs', 'https://api.postqron.com')).toBe('https://api.postqron.com/v1/jobs')
  })
})

describe('buildQuery', () => {
  it('serializza i parametri valorizzati', () => {
    expect(buildQuery({ page: 2, enabled: true })).toBe('?page=2&enabled=true')
  })

  it('scarta i parametri non valorizzati', () => {
    expect(buildQuery({ status: undefined, cursor: null, q: '' })).toBe('')
  })

  it('codifica i caratteri speciali', () => {
    expect(buildQuery({ q: 'job fallito' })).toBe('?q=job+fallito')
  })
})
