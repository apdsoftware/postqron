import { describe, expect, it } from 'vitest'

import type { JobDraft, JobResponse } from '~/utils/jobs'
import { JOB_LIMITS } from '~/utils/job-contract'
import {
  draftFromJob,
  draftIntervalSeconds,
  draftSchedule,
  emptyDraft,
  issueFor,
  issuesFromServer,
  jobPayload,
  validateDraft,
} from '~/utils/jobs'

/** Un modulo valido, da cui ogni prova cambia una cosa sola. */
function valido(overrides: Partial<JobDraft> = {}): JobDraft {
  return {
    ...emptyDraft(),
    name: 'daily-digest',
    schedule: '0 9 * * *',
    timezone: 'Europe/Rome',
    url: 'https://api.esempio.test/tasks/digest',
    ...overrides,
  }
}

function codici(draft: JobDraft): string[] {
  return validateDraft(draft).map(issue => `${issue.field}:${issue.code}`)
}

const jobDalBackend: JobResponse = {
  id: '0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c',
  name: 'daily-digest',
  description: 'Il riepilogo delle nove',
  schedule: '0 9 * * *',
  every: null,
  timezone: 'Europe/Rome',
  environments: ['production'],
  request: {
    url: 'https://api.esempio.test/tasks/digest',
    method: 'POST',
    headers: { Authorization: 'Bearer x' },
    body: '{"kind":"daily"}',
  },
  timeout: '1m',
  retries: { max: 5, backoff: 'exponential' },
  on_overlap: 'skip',
  alerts: { on_failure: ['email'] },
  enabled: true,
  next_run_at: '2026-08-18T07:00:00Z',
  created_at: '2026-08-17T09:00:00Z',
  updated_at: '2026-08-17T09:00:00Z',
}

// ------------------------------------------------------- andata e ritorno

describe('dal job al modulo e ritorno', () => {
  it('conserva ciò che il backend ha mandato', () => {
    const draft = draftFromJob(jobDalBackend)

    expect(draft.mode).toBe('cron')
    expect(draft.schedule).toBe('0 9 * * *')
    expect(draft.timezone).toBe('Europe/Rome')
    // `1m` diventa 60 secondi nel modulo, e torna `60s` nel payload: le due
    // scritture sono lo stesso valore, e il backend riscrive la sua.
    expect(draft.timeoutSeconds).toBe('60')
    expect(draft.headers).toEqual([{ name: 'Authorization', value: 'Bearer x' }])
    expect(draft.body).toBe('{"kind":"daily"}')

    expect(jobPayload(draft)).toMatchObject({
      name: 'daily-digest',
      schedule: '0 9 * * *',
      every: null,
      timeout: '60s',
      request: {
        url: 'https://api.esempio.test/tasks/digest',
        method: 'POST',
        headers: { Authorization: 'Bearer x' },
        body: '{"kind":"daily"}',
      },
      retries: { max: 5, backoff: 'exponential' },
    })
  })

  it('legge un job a intervallo nell\'unità più leggibile', () => {
    // `3600` come «1 ora» e non «3600 secondi»: sono lo stesso valore, ma il
    // secondo invita a correggerlo in 3601 invece che in due ore.
    const orario = draftFromJob({ ...jobDalBackend, schedule: null, every: '1h' })
    expect(orario.mode).toBe('interval')
    expect(orario.everyAmount).toBe('1')
    expect(orario.everyUnit).toBe('h')
    expect(draftIntervalSeconds(orario)).toBe(3600)

    const secondi = draftFromJob({ ...jobDalBackend, schedule: null, every: '90s' })
    expect(secondi.everyAmount).toBe('90')
    expect(secondi.everyUnit).toBe('s')
  })

  it('commuta modalità con un null esplicito, non con un campo assente', () => {
    // È l'unico modo di dismettere una modalità: `optional[T]` distingue «campo
    // assente» da «campo a null», e senza il secondo il backend lascerebbe
    // l'espressione dov'era.
    const aIntervallo = jobPayload(valido({ mode: 'interval', everyAmount: '10', everyUnit: 's' }))
    expect(aIntervallo).toMatchObject({ schedule: null, every: '10s' })

    const aCron = jobPayload(valido({ mode: 'cron' }))
    expect(aCron).toMatchObject({ schedule: '0 9 * * *', every: null })
  })

  it('non sostituisce i valori che non conosce', () => {
    // Un metodo che questo bundle non conosce è un backend più nuovo di lui:
    // rimpiazzarlo con POST cambierebbe il job di qualcun altro senza dirlo.
    const draft = draftFromJob({
      ...jobDalBackend,
      request: { ...jobDalBackend.request, method: 'QUERY' },
    })
    expect(draft.method).toBe('QUERY')
    expect(jobPayload(draft).request).toMatchObject({ method: 'QUERY' })
  })

  it('un job nuovo parte dai predefiniti di jobs.NewJob()', () => {
    const draft = emptyDraft()
    expect(draft.timezone).toBe('UTC')
    expect(draft.timeoutSeconds).toBe('30')
    expect(draft.maxRetries).toBe('3')
    expect(draft.overlapPolicy).toBe('skip')
    expect(draft.environments).toEqual(['production'])
    expect(draft.alertOnFailure).toEqual(['email'])
  })
})

// -------------------------------------------------------------- validazione

describe('validazione immediata', () => {
  it('un modulo compilato bene non ha rilievi', () => {
    expect(codici(valido())).toEqual([])
    expect(codici(valido({ mode: 'interval', everyAmount: '10', everyUnit: 's', schedule: '' })))
      .toEqual([])
  })

  it('il nome è l\'identità del job, e ne ha il formato', () => {
    expect(codici(valido({ name: '' }))).toContain('name:required')
    expect(codici(valido({ name: 'a'.repeat(JOB_LIMITS.maxNameLength + 1) })))
      .toContain('name:tooLong')
    expect(codici(valido({ name: 'con spazi' }))).toContain('name:invalidName')
    expect(codici(valido({ name: '-inizia-male' }))).toContain('name:invalidName')
    // Al limite esatto passa: il tetto è incluso, come `len(j.Name) > Max`.
    expect(codici(valido({ name: `a${'b'.repeat(JOB_LIMITS.maxNameLength - 1)}` })))
      .toEqual([])
  })

  it('dice quale campo dell\'espressione non va', () => {
    // È ciò che il backend non può dire senza mandare una frase in italiano: il
    // suo `invalid_schedule` ha il dettaglio dentro il messaggio.
    expect(issueFor(validateDraft(valido({ schedule: '60 * * * *' })), 'schedule'))
      .toMatchObject({ code: 'scheduleField', value: '60' })
    expect(codici(valido({ schedule: '' }))).toContain('schedule:scheduleRequired')
    expect(codici(valido({ schedule: '0 9 * *' }))).toContain('schedule:scheduleFieldCount')
    expect(codici(valido({ schedule: '@daily' }))).toContain('schedule:scheduleMacro')
  })

  it('il fuso è validato in entrambe le modalità', () => {
    expect(codici(valido({ timezone: 'Europa/Roma' }))).toContain('timezone:unknownTimezone')
    expect(codici(valido({ timezone: 'Local' }))).toContain('timezone:localTimezone')
    expect(codici(valido({
      mode: 'interval', everyAmount: '10', everyUnit: 's', schedule: '', timezone: 'Europa/Roma',
    }))).toContain('timezone:unknownTimezone')
  })

  it('l\'intervallo dev\'essere un numero intero di secondi', () => {
    for (const amount of ['', '0', '1.5', 'dieci', '-3']) {
      expect(codici(valido({ mode: 'interval', schedule: '', everyAmount: amount })), amount)
        .toContain('every:invalidInterval')
    }
  })

  it('il bersaglio è HTTP, e nient\'altro', () => {
    expect(codici(valido({ url: '' }))).toContain('request.url:required')
    expect(codici(valido({ url: 'non un url' }))).toContain('request.url:invalidUrl')
    // `jobs_url_scheme_check`: Postqron non esegue comandi né container.
    expect(codici(valido({ url: 'ftp://esempio.test/x' })))
      .toContain('request.url:unsupportedScheme')
    expect(codici(valido({ url: `https://esempio.test/${'a'.repeat(JOB_LIMITS.maxUrlLength)}` })))
      .toContain('request.url:tooLong')
  })

  it('non anticipa il blocco dei bersagli, che vive altrove', () => {
    // R38 si applica all'apertura della connessione, non guardando una stringa:
    // rifiutare qui `localhost` sarebbe una regola che il browser si inventa, e
    // il messaggio del server è deliberatamente vago per non rispondere a
    // «questo nome, dalla vostra rete, risolve internamente?».
    expect(codici(valido({ url: 'http://localhost:8080/hook' }))).toEqual([])
    expect(codici(valido({ url: 'http://169.254.169.254/latest/meta-data' }))).toEqual([])
  })

  it('gli header seguono le regole che l\'esecutore impone', () => {
    const con = (name: string, value = 'v'): string[] =>
      codici(valido({ headers: [{ name, value }] }))

    expect(con('X Sbagliato')).toContain('request.headers:invalidHeaderName')
    // `Host` cambierebbe il bersaglio effettivo senza cambiare l'URL.
    expect(con('Host')).toContain('request.headers:reservedHeader')
    expect(con('content-length')).toContain('request.headers:reservedHeader')
    // Un a capo è una richiesta di iniettare header nella chiamata che faremmo
    // noi, dal nostro IP.
    expect(con('X-Ok', 'uno\ndue')).toContain('request.headers:headerNewline')
    expect(con('X-Ok', 'v'.repeat(JOB_LIMITS.maxHeaderValueLength + 1)))
      .toContain('request.headers:headerTooLong')
  })

  it('rifiuta due header con lo stesso nome, e solo per non mentire', () => {
    /*
     * È l'unica regola che il backend non ha, ed è dichiarata: nel corpo JSON
     * gli header sono una mappa, quindi la seconda riga sovrascriverebbe la
     * prima durante la conversione. Non è una validazione più stretta della
     * richiesta — quella che ne risulta il server la accetta — è il rifiuto di
     * inviare qualcosa di diverso da ciò che è scritto sullo schermo.
     */
    const doppio = valido({
      headers: [{ name: 'X-Token', value: 'uno' }, { name: 'x-token', value: 'due' }],
    })
    expect(codici(doppio)).toContain('request.headers:duplicateHeader')
    // E la mappa che ne risulterebbe perde davvero una riga: è il fatto che
    // giustifica il rifiuto.
    expect(Object.keys(jobPayload(doppio).request as Record<string, never>)).toContain('headers')
  })

  it('le righe vuote degli header non contano', () => {
    // La riga in fondo pronta da riempire è parte del modulo, non un errore.
    expect(codici(valido({ headers: [{ name: '', value: '' }] }))).toEqual([])
    expect(jobPayload(valido({ headers: [{ name: '', value: '' }] })))
      .toMatchObject({ request: { headers: {} } })
  })

  it('il corpo si misura in byte, come il backend', () => {
    // `String.length` conta unità UTF-16: un corpo pieno di accenti passerebbe
    // qui e verrebbe rifiutato dal server, che è la direzione da evitare.
    const quasi = 'à'.repeat(JOB_LIMITS.maxBodyLength / 2)
    expect(codici(valido({ body: quasi }))).toEqual([])
    expect(codici(valido({ body: `${quasi}a` }))).toContain('request.body:bodyTooLong')
  })

  it('timeout e tentativi stanno nei confini del backend', () => {
    expect(codici(valido({ timeoutSeconds: '0' }))).toContain('timeout:timeoutRange')
    expect(codici(valido({ timeoutSeconds: '301' }))).toContain('timeout:timeoutRange')
    expect(codici(valido({ timeoutSeconds: '1' }))).toEqual([])
    expect(codici(valido({ timeoutSeconds: '300' }))).toEqual([])
    // «Un numero intero di secondi»: `jobs.every_seconds` e `timeout_seconds`
    // sono interi, e troncare in silenzio farebbe partire il job con una
    // cadenza diversa da quella scritta.
    expect(codici(valido({ timeoutSeconds: '1.5' }))).toContain('timeout:timeoutWhole')

    expect(codici(valido({ maxRetries: '11' }))).toContain('retries.max:retriesRange')
    expect(codici(valido({ maxRetries: '0' }))).toEqual([])
    expect(codici(valido({ maxRetries: '10' }))).toEqual([])
  })

  it('serve almeno un ambiente', () => {
    expect(codici(valido({ environments: [] }))).toContain('environments:environmentsRequired')
  })
})

// ------------------------------------------------- i rifiuti che arrivano dal server

describe('rifiuti del backend', () => {
  it('finiscono accanto al campo giusto senza tradurre i nomi', () => {
    // I due vocabolari dei campi sono lo stesso apposta: `request.url` qui e
    // `request.url` là, così non serve una tabella in mezzo.
    const issues = issuesFromServer([
      { field: 'request.url', code: 'target_not_allowed' },
      { field: 'name', code: 'too_long' },
      { field: 'timeout', code: 'out_of_range' },
    ])
    expect(issues).toEqual([
      { field: 'request.url', code: 'targetNotAllowed' },
      { field: 'name', code: 'tooLong' },
      { field: 'timeout', code: 'timeoutRange' },
    ])
  })

  it('un codice sconosciuto non diventa un messaggio inventato', () => {
    expect(issuesFromServer([{ field: 'name', code: 'codice_di_domani' }]))
      .toEqual([{ field: 'name', code: 'rejected' }])
  })

  it('scarta i campi che non appartengono a nessuna casella', () => {
    // Un messaggio che galleggia in fondo al modulo non dice cosa correggere.
    expect(issuesFromServer([{ field: 'campo.inventato', code: 'required' }])).toEqual([])
  })
})

// --------------------------------------------------------------- anteprima

describe('la schedulazione che l\'anteprima riceve', () => {
  it('è quella del modulo, con il fuso in entrambe le modalità', () => {
    expect(draftSchedule(valido()))
      .toEqual({ expression: '0 9 * * *', timezone: 'Europe/Rome' })
    expect(draftSchedule(valido({ mode: 'interval', everyAmount: '5', everyUnit: 'm' })))
      .toEqual({ everySeconds: 300, timezone: 'Europe/Rome' })
  })

  it('non inventa un intervallo quando il campo non si legge', () => {
    // Zero significa «modalità non dichiarata», e l'anteprima mostra il proprio
    // stato invece di un elenco di orari sbagliati.
    expect(draftSchedule(valido({ mode: 'interval', everyAmount: 'dieci' })))
      .toEqual({ everySeconds: 0, timezone: 'Europe/Rome' })
  })
})
