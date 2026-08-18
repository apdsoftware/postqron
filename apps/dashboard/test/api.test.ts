import { describe, expect, it } from 'vitest'

import { ApiError, apiErrorKind, apiUrl, buildQuery, parseErrorCode } from '../utils/api'

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

describe('apiErrorKind', () => {
  it('distingue i codici su cui cambia cosa può fare l\'utente', () => {
    expect(apiErrorKind(401)).toBe('unauthorized')
    expect(apiErrorKind(403)).toBe('forbidden')
    expect(apiErrorKind(404)).toBe('notFound')
  })

  it('raccoglie il resto dei 4xx come richiesta non valida', () => {
    expect(apiErrorKind(400)).toBe('invalid')
    expect(apiErrorKind(422)).toBe('invalid')
    expect(apiErrorKind(429)).toBe('invalid')
  })

  it('tratta i 5xx come guasto del server', () => {
    expect(apiErrorKind(500)).toBe('server')
    expect(apiErrorKind(502)).toBe('server')
    expect(apiErrorKind(504)).toBe('server')
  })

  it('tratta un 3xx arrivato fin qui come guasto del server', () => {
    // `fetch` segue i reindirizzamenti da solo: se uno arriva a questa funzione
    // è una configurazione rotta, non un esito da ignorare.
    expect(apiErrorKind(302)).toBe('server')
  })
})

describe('parseErrorCode', () => {
  it('legge il codice dichiarato dal backend', () => {
    expect(parseErrorCode('{"error":{"code":"weak_password","message":"…"}}')).toBe('weak_password')
  })

  it('un corpo che non è la forma attesa non ha codice', () => {
    // Sono i casi veri: il 502 di un proxy davanti al backend è HTML, un 401
    // può non avere corpo affatto, e nessuno dei due deve far lanciare niente.
    expect(parseErrorCode('')).toBeNull()
    expect(parseErrorCode('<html>502 Bad Gateway</html>')).toBeNull()
    expect(parseErrorCode('null')).toBeNull()
    expect(parseErrorCode('{"error":"non_un_oggetto"}')).toBeNull()
    expect(parseErrorCode('{"error":{"code":42}}')).toBeNull()
    expect(parseErrorCode('{"error":{"code":""}}')).toBeNull()
  })
})

describe('ApiError', () => {
  it('un guasto di rete non ha codice', () => {
    const error = ApiError.network('https://api.postqron.com/v1/jobs', new Error('Failed to fetch'))

    expect(error.kind).toBe('network')
    expect(error.status).toBeNull()
  })

  it('una risposta fuori dal 2xx porta con sé il proprio codice', () => {
    const error = ApiError.fromStatus('https://api.postqron.com/v1/jobs', 401, 'Unauthorized')

    expect(error.kind).toBe('unauthorized')
    expect(error.status).toBe(401)
    // Nessun codice dichiarato: la categoria HTTP c'è sempre, il codice no.
    expect(error.code).toBeNull()
  })

  it('conserva il codice del backend accanto alla categoria', () => {
    // I due campi rispondono a domande diverse: `kind` dice cosa può fare
    // l'utente, `code` quale regola è scattata. Un modulo ha bisogno del
    // secondo per indicare *quale* campo rifare.
    const error = ApiError.fromStatus('https://api.postqron.com/auth/register', 400, '', 'weak_password')

    expect(error.kind).toBe('invalid')
    expect(error.code).toBe('weak_password')
  })

  it('resta un Error, così un catch senza filtri continua a funzionare', () => {
    expect(ApiError.fromStatus('https://api.postqron.com/v1/jobs', 500, '')).toBeInstanceOf(Error)
  })

  it('scrive nel messaggio dove e come, perché è quello che serve in console', () => {
    // Il messaggio non si mostra mai all'utente — è in inglese, e le lingue sono
    // cinque. Serve a chi sviluppa, e deve dire l'indirizzo: «richiesta fallita»
    // senza l'indirizzo manda a cercare in tutta l'applicazione.
    const error = ApiError.fromStatus('https://api.postqron.com/v1/jobs', 503, 'Service Unavailable')

    expect(error.message).toContain('https://api.postqron.com/v1/jobs')
    expect(error.message).toContain('503')
  })
})
