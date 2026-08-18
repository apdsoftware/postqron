import { describe, expect, it } from 'vitest'

import type { Occurrence, ScheduleSpec } from '~/utils/schedule'
import {
  compileSchedule,
  formatDurationSeconds,
  nextOccurrences,
  parseDurationSeconds,
  resolveTimezone,
} from '~/utils/schedule'

/**
 * L'anteprima delle esecuzioni deve dire **gli stessi istanti** che il motore
 * eseguirà.
 *
 * Non è una similitudine: i casi qui sotto sono quelli di
 * `services/api/internal/schedule/{next,dst,every}_test.go`, con gli stessi
 * orari attesi, e sono duplicati apposta. Un'anteprima che dicesse un istante e
 * un motore che ne eseguisse un altro sarebbe peggio di nessuna anteprima —
 * sarebbe una promessa — e i due punti in cui è più facile che divergano sono
 * proprio quelli in cui le regole di Postqron **non** sono quelle che una
 * libreria applicherebbe da sé: l'ora che non esiste e l'ora che accade due
 * volte.
 *
 * Se un giorno il pacchetto Go cambiasse una di queste regole, questa suite
 * resterebbe verde ed è il suo unico limite dichiarato: il presidio è che i due
 * elenchi si somiglino abbastanza da rendere ovvio, a chi tocca l'uno, che
 * esiste l'altro.
 */

/** Le prime `n` occorrenze dopo `from`, in RFC 3339 UTC — come `sequenza`. */
function sequenza(spec: ScheduleSpec, from: string, n: number): string[] {
  const occorrenze = nextOccurrences(spec, new Date(from), n)
  if (!Array.isArray(occorrenze)) {
    throw new Error(`schedulazione non compilata: ${occorrenze.code} ${occorrenze.field ?? ''}`)
  }
  return occorrenze.map(occurrence => iso(occurrence.at))
}

function iso(date: Date): string {
  return date.toISOString().replace(/\.000Z$/, 'Z')
}

function cron(expression: string, timezone = 'UTC'): ScheduleSpec {
  return { expression, timezone }
}

function occorrenze(spec: ScheduleSpec, from: string, n: number): Occurrence[] {
  const result = nextOccurrences(spec, new Date(from), n)
  if (!Array.isArray(result)) throw new Error(`schedulazione non compilata: ${result.code}`)
  return result
}

// ------------------------------------------------------------------ parsing

describe('compilazione della schedulazione', () => {
  it('rifiuta le due modalità insieme e la loro assenza', () => {
    // È il vincolo `jobs_schedule_xor_every_check`, e le due letture sono
    // distinte perché il rimedio è diverso: togliere un campo o aggiungerne uno.
    expect(compileSchedule({ expression: '0 9 * * *', everySeconds: 60 }))
      .toMatchObject({ code: 'bothModes' })
    expect(compileSchedule({})).toMatchObject({ code: 'noMode' })
  })

  it('accetta la sintassi del crontab classico', () => {
    const valide = [
      '* * * * *',
      '0 9 * * *',
      '*/15 * * * *',
      '0 0 1 JAN *',
      '0 9 * * MON-FRI',
      '0 0 13 * FRI',
      '5,10,15 * * * *',
      '0-30/10 * * * *',
      '0 0 * * 7',
    ]
    for (const espressione of valide) {
      expect(compileSchedule(cron(espressione)), espressione).toHaveProperty('kind', 'cron')
    }
  })

  it('rifiuta ciò che il backend rifiuta, dicendo quale campo', () => {
    // I codici sono quelli che `internal/schedule` distingue, perché è su quelli
    // che cambia cosa c'è da scrivere al posto di ciò che si è scritto.
    expect(compileSchedule(cron('@daily'))).toMatchObject({ code: 'macro' })
    expect(compileSchedule(cron('0 9 * *'))).toMatchObject({ code: 'fieldCount' })
    expect(compileSchedule(cron('0 0 9 * * *'))).toMatchObject({ code: 'fieldCount' })
    expect(compileSchedule(cron('60 * * * *'))).toMatchObject({ code: 'invalidField', field: 'minute' })
    expect(compileSchedule(cron('* 24 * * *'))).toMatchObject({ code: 'invalidField', field: 'hour' })
    expect(compileSchedule(cron('* * 0 * *'))).toMatchObject({ code: 'invalidField', field: 'dayOfMonth' })
    expect(compileSchedule(cron('* * * XYZ *'))).toMatchObject({ code: 'invalidField', field: 'month' })
    expect(compileSchedule(cron('* * * * FUN'))).toMatchObject({ code: 'invalidField', field: 'dayOfWeek' })
    // Il passo si applica a `*` o a un intervallo: `5/15` non vuol dire niente
    // di definito, e accettarlo con un'interpretazione a caso è peggio.
    expect(compileSchedule(cron('5/15 * * * *'))).toMatchObject({ code: 'invalidField', field: 'minute' })
    // Intervallo rovesciato: `FRI-MON` sembra sensato e non lo è.
    expect(compileSchedule(cron('* * * * FRI-MON'))).toMatchObject({ code: 'invalidField' })
    expect(compileSchedule(cron('0-30/0 * * * *'))).toMatchObject({ code: 'invalidField' })
  })

  it('valida il fuso anche in modalità a intervallo', () => {
    // Il fuso non serve a un intervallo, ma se è stato scritto va controllato:
    // un refuso deve emergere subito, non il giorno in cui quel job passa a
    // `schedule`.
    expect(compileSchedule({ everySeconds: 60, timezone: 'Europa/Roma' }))
      .toMatchObject({ code: 'unknownTimezone' })
    expect(compileSchedule({ everySeconds: 60, timezone: 'Europe/Rome' }))
      .toHaveProperty('kind', 'interval')
  })

  it('rifiuta «Local», che dipende dalla macchina', () => {
    expect(resolveTimezone('Local')).toMatchObject({ code: 'localTimezone' })
    expect(resolveTimezone('')).toBe('UTC')
    expect(resolveTimezone(undefined)).toBe('UTC')
    expect(resolveTimezone('Europe/Rome')).toBe('Europe/Rome')
  })

  it('rifiuta gli intervalli che il database non memorizzerebbe', () => {
    // Un intervallo negativo è un intervallo scritto male, non una
    // schedulazione assente: è la stessa distinzione di `schedule.Parse`, dove
    // `hasEvery` guarda il diverso-da-zero e non il positivo.
    expect(compileSchedule({ everySeconds: -1 })).toMatchObject({ code: 'invalidInterval' })
    expect(compileSchedule({ everySeconds: 1.5 })).toMatchObject({ code: 'invalidInterval' })
    expect(compileSchedule({ everySeconds: 2_147_483_648 })).toMatchObject({ code: 'invalidInterval' })
    expect(compileSchedule({ everySeconds: 1 })).toHaveProperty('kind', 'interval')
  })
})

// -------------------------------------------------------- le occorrenze

describe('prossime occorrenze in modalità cron', () => {
  it('giornaliero', () => {
    expect(sequenza(cron('0 9 * * *'), '2026-08-17T00:00:00Z', 3)).toEqual([
      '2026-08-17T09:00:00Z', '2026-08-18T09:00:00Z', '2026-08-19T09:00:00Z',
    ])
  })

  it('è strettamente successivo all\'istante di partenza', () => {
    // Partire esattamente da un'occorrenza dà la successiva, mai la stessa:
    // senza, l'anteprima ripeterebbe la prima riga cinque volte.
    expect(sequenza(cron('0 9 * * *'), '2026-08-17T09:00:00Z', 1))
      .toEqual(['2026-08-18T09:00:00Z'])
  })

  it('ignora i secondi dell\'istante di partenza', () => {
    for (const da of ['2026-08-17T09:00:00Z', '2026-08-17T09:00:00.001Z', '2026-08-17T09:00:59Z']) {
      expect(sequenza(cron('* * * * *'), da, 1), da).toEqual(['2026-08-17T09:01:00Z'])
    }
  })

  it('con passo', () => {
    expect(sequenza(cron('*/15 * * * *'), '2026-08-17T09:07:00Z', 4)).toEqual([
      '2026-08-17T09:15:00Z', '2026-08-17T09:30:00Z',
      '2026-08-17T09:45:00Z', '2026-08-17T10:00:00Z',
    ])
  })

  it('giorni feriali', () => {
    // 2026-08-17 è un lunedì; il venerdì è il 21, il lunedì dopo il 24.
    expect(sequenza(cron('0 9 * * MON-FRI'), '2026-08-20T12:00:00Z', 3)).toEqual([
      '2026-08-21T09:00:00Z', '2026-08-24T09:00:00Z', '2026-08-25T09:00:00Z',
    ])
  })

  it('domenica si scrive 0 oppure 7', () => {
    const zero = sequenza(cron('0 0 * * 0'), '2026-08-17T00:00:00Z', 3)
    expect(sequenza(cron('0 0 * * 7'), '2026-08-17T00:00:00Z', 3)).toEqual(zero)
    // 2026-08-23 è una domenica.
    expect(zero).toEqual(['2026-08-23T00:00:00Z', '2026-08-30T00:00:00Z', '2026-09-06T00:00:00Z'])
  })

  it('due campi dei giorni ristretti valgono in unione, non in intersezione', () => {
    // `0 0 13 * FRI` è «il 13 del mese oppure ogni venerdì», non «i venerdì 13».
    // Novembre 2026: venerdì 6, 13, 20, 27.
    expect(sequenza(cron('0 0 13 * FRI'), '2026-11-01T00:00:00Z', 4)).toEqual([
      '2026-11-06T00:00:00Z', '2026-11-13T00:00:00Z',
      '2026-11-20T00:00:00Z', '2026-11-27T00:00:00Z',
    ])
  })

  it('con un solo campo dei giorni ristretto vale quello', () => {
    expect(sequenza(cron('0 0 * * MON'), '2026-11-01T00:00:00Z', 3)).toEqual([
      '2026-11-02T00:00:00Z', '2026-11-09T00:00:00Z', '2026-11-16T00:00:00Z',
    ])
    expect(sequenza(cron('0 0 13 * *'), '2026-11-01T00:00:00Z', 3)).toEqual([
      '2026-11-13T00:00:00Z', '2026-12-13T00:00:00Z', '2027-01-13T00:00:00Z',
    ])
  })

  it('il passo sui giorni restringe, quindi fa scattare l\'unione', () => {
    // Novembre 2026: giorni 1, 11, 21, 31 dal passo; lunedì 2, 9, 16, 23, 30.
    expect(sequenza(cron('0 0 */10 * MON'), '2026-11-01T00:00:00Z', 5)).toEqual([
      '2026-11-02T00:00:00Z', '2026-11-09T00:00:00Z', '2026-11-11T00:00:00Z',
      '2026-11-16T00:00:00Z', '2026-11-21T00:00:00Z',
    ])
  })

  it('anno bisestile', () => {
    expect(sequenza(cron('0 0 29 2 *'), '2026-01-01T00:00:00Z', 2))
      .toEqual(['2028-02-29T00:00:00Z', '2032-02-29T00:00:00Z'])
  })

  it('una data impossibile non ha occorrenze', () => {
    // `0 0 30 2 *` non parte mai. È il caso che l'anteprima esiste per rendere
    // visibile **prima** di salvare: una lista vuota è un'informazione.
    expect(sequenza(cron('0 0 30 2 *'), '2026-01-01T00:00:00Z', 3)).toEqual([])
  })

  it('cambio di mese e di anno', () => {
    expect(sequenza(cron('0 0 1 * *'), '2026-11-15T00:00:00Z', 3)).toEqual([
      '2026-12-01T00:00:00Z', '2027-01-01T00:00:00Z', '2027-02-01T00:00:00Z',
    ])
  })
})

describe('il fuso è quello del job, non del browser', () => {
  it('sposta l\'istante ma non l\'orario da parete', () => {
    // Le 09:00 di Roma in agosto sono le 07:00 UTC, in gennaio le 08:00.
    expect(sequenza(cron('0 9 * * *', 'Europe/Rome'), '2026-08-17T00:00:00Z', 1))
      .toEqual(['2026-08-17T07:00:00Z'])
    expect(sequenza(cron('0 9 * * *', 'Europe/Rome'), '2026-01-15T00:00:00Z', 1))
      .toEqual(['2026-01-15T08:00:00Z'])
  })

  it('regge uno scostamento non intero', () => {
    // Calcutta è a +05:30 e non ha ora legale.
    expect(sequenza(cron('30 9 * * *', 'Asia/Kolkata'), '2026-08-17T00:00:00Z', 2))
      .toEqual(['2026-08-17T04:00:00Z', '2026-08-18T04:00:00Z'])
  })
})

// -------------------------------------------------------------- ora legale

describe('l\'ora che non esiste', () => {
  it('non salta l\'occorrenza: la sposta al primo istante che esiste', () => {
    expect(sequenza(cron('30 2 * * *', 'Europe/Rome'), '2026-03-27T12:00:00Z', 3)).toEqual([
      '2026-03-28T01:30:00Z', // 02:30 CET, giorno normale
      '2026-03-29T01:00:00Z', // le 02:30 non esistono: si parte alle 03:00 CEST
      '2026-03-30T00:30:00Z', // 02:30 CEST, giorno normale
    ])
  })

  it('fa collassare in una sola le occorrenze dentro il buco', () => {
    expect(sequenza(cron('*/15 2 * * *', 'Europe/Rome'), '2026-03-28T23:00:00Z', 5)).toEqual([
      '2026-03-29T01:00:00Z', // una sola volta, all'istante del salto
      '2026-03-30T00:00:00Z',
      '2026-03-30T00:15:00Z',
      '2026-03-30T00:30:00Z',
      '2026-03-30T00:45:00Z',
    ])
  })

  it('regge i salti irregolari e gli altri emisferi', () => {
    expect(sequenza(cron('15 2 * * *', 'Australia/Lord_Howe'), '2026-10-03T00:00:00Z', 1))
      .toEqual(['2026-10-03T15:30:00Z']) // buco di mezz'ora
    expect(sequenza(cron('0 3 * * *', 'Pacific/Chatham'), '2026-09-26T00:00:00Z', 1))
      .toEqual(['2026-09-26T14:00:00Z']) // buco da 02:45 a 03:45
    expect(sequenza(cron('30 2 * * *', 'Australia/Sydney'), '2026-10-03T00:00:00Z', 1))
      .toEqual(['2026-10-03T16:00:00Z'])
    expect(sequenza(cron('30 2 * * *', 'America/New_York'), '2026-03-07T12:00:00Z', 1))
      .toEqual(['2026-03-08T07:00:00Z'])
  })

  it('regge il salto che scavalca la mezzanotte', () => {
    // 2026-03-08 all'Avana l'orologio passa dalle 23:59 del 7 all'01:00 dell'8:
    // la mezzanotte dell'8 non esiste, ed è l'orario più usato che esista.
    expect(sequenza(cron('0 0 * * *', 'America/Havana'), '2026-03-06T12:00:00Z', 3)).toEqual([
      '2026-03-07T05:00:00Z',
      '2026-03-08T05:00:00Z',
      '2026-03-09T04:00:00Z',
    ])
  })

  it('dichiara che l\'occorrenza è stata spostata', () => {
    // L'anteprima deve poterlo **dire**: un'occorrenza che si sposta di
    // mezz'ora senza spiegazione sembra un difetto del prodotto.
    const risolte = occorrenze(cron('30 2 * * *', 'Europe/Rome'), '2026-03-28T23:00:00Z', 2)
    expect(risolte[0]?.resolution).toBe('gap')
    expect(risolte[1]?.resolution).toBe('exact')
  })
})

describe('l\'ora che accade due volte', () => {
  it('esegue solo alla prima', () => {
    expect(sequenza(cron('30 2 * * *', 'Europe/Rome'), '2026-10-23T12:00:00Z', 3)).toEqual([
      '2026-10-24T00:30:00Z', // 02:30 CEST, giorno normale
      '2026-10-25T00:30:00Z', // prima passata; la seconda (01:30Z) è saltata
      '2026-10-26T01:30:00Z', // 02:30 CET, giorno normale
    ])
  })

  it('salta interamente l\'ora ripetuta', () => {
    // Fra le 02:30 e le 03:00 dell'orologio passano novanta minuti veri.
    expect(sequenza(cron('*/30 * * * *', 'Europe/Rome'), '2026-10-24T23:30:00Z', 4)).toEqual([
      '2026-10-25T00:00:00Z', // 02:00 CEST
      '2026-10-25T00:30:00Z', // 02:30 CEST
      '2026-10-25T02:00:00Z', // 03:00 CET — novanta minuti dopo
      '2026-10-25T02:30:00Z',
    ])
  })

  it('non torna indietro se si guarda dentro la seconda passata', () => {
    // L'orario da parete 02:30 combacia ancora, ma il suo istante — la prima
    // passata — è già passato: l'occorrenza va scartata.
    expect(sequenza(cron('30 2 * * *', 'Europe/Rome'), '2026-10-25T01:10:00Z', 1))
      .toEqual(['2026-10-26T01:30:00Z'])
  })

  it('vale negli altri fusi', () => {
    expect(sequenza(cron('30 1 * * *', 'America/New_York'), '2026-10-31T12:00:00Z', 1))
      .toEqual(['2026-11-01T05:30:00Z'])
    expect(sequenza(cron('45 1 * * *', 'Australia/Lord_Howe'), '2026-04-04T00:00:00Z', 1))
      .toEqual(['2026-04-04T14:45:00Z'])
  })

  it('dichiara che l\'orario era ambiguo', () => {
    // Il 25 ottobre 2026 le 02:30 a Roma accadono due volte: si parte alla
    // prima, e l'anteprima deve poterlo dire invece di mostrare un orario che
    // il giorno dopo non si ripete uguale.
    const risolte = occorrenze(cron('30 2 * * *', 'Europe/Rome'), '2026-10-24T12:00:00Z', 2)
    expect(risolte[0]?.resolution).toBe('ambiguous')
    expect(risolte[1]?.resolution).toBe('exact')
  })
})

// ------------------------------------------------------------- intervallo

describe('modalità a intervallo', () => {
  it('è ancorata all\'epoch, non al salvataggio', () => {
    // È l'errore che sembra giusto: `every: 1h` salvato alle 14:37 scocca alle
    // 15:00 UTC, non alle 15:37.
    expect(sequenza({ everySeconds: 3600 }, '2026-08-17T14:37:12Z', 3)).toEqual([
      '2026-08-17T15:00:00Z', '2026-08-17T16:00:00Z', '2026-08-17T17:00:00Z',
    ])
    expect(sequenza({ everySeconds: 10 }, '2026-08-17T14:37:12Z', 3)).toEqual([
      '2026-08-17T14:37:20Z', '2026-08-17T14:37:30Z', '2026-08-17T14:37:40Z',
    ])
  })

  it('non si sposta cambiando fuso dichiarato', () => {
    // Un intervallo non ha un orologio da parete: chi ne vuole uno usa `schedule`.
    const roma = sequenza({ everySeconds: 3600, timezone: 'Europe/Rome' }, '2026-08-17T14:37:00Z', 2)
    const calcutta = sequenza({ everySeconds: 3600, timezone: 'Asia/Kolkata' }, '2026-08-17T14:37:00Z', 2)
    expect(roma).toEqual(calcutta)
    expect(roma).toEqual(['2026-08-17T15:00:00Z', '2026-08-17T16:00:00Z'])
  })

  it('ignora i cambi d\'ora', () => {
    // Nel giorno in cui l'orologio di Roma torna indietro, `every: 1h` produce
    // venticinque occorrenze locali e `0 * * * *` ventiquattro: è la differenza
    // fra «ogni ora» e «a ogni ora piena dell'orologio», ed è il motivo per cui
    // le due modalità esistono entrambe. La griglia dell'intervallo non se ne
    // accorge nemmeno: resta ogni 3600 secondi esatti.
    const partenza = new Date('2026-10-24T22:00:00Z')
    const griglia = occorrenze({ everySeconds: 3600, timezone: 'Europe/Rome' }, partenza.toISOString(), 6)
    for (let i = 0; i < griglia.length; i += 1) {
      expect(iso(griglia[i]!.at)).toBe(iso(new Date(partenza.getTime() + (i + 1) * 3600_000)))
    }

    // Il cron, nello stesso intervallo, salta l'ora ripetuta: fra le 02:30 e le
    // 03:00 dell'orologio passano novanta minuti.
    const orologio = sequenza(cron('0 * * * *', 'Europe/Rome'), '2026-10-24T23:30:00Z', 4)
    expect(orologio).toEqual([
      '2026-10-25T00:00:00Z', // 02:00 CEST
      '2026-10-25T02:00:00Z', // 03:00 CET — l'ora ripetuta è saltata
      '2026-10-25T03:00:00Z',
      '2026-10-25T04:00:00Z',
    ])
  })

  it('ha sempre un\'occorrenza', () => {
    expect(sequenza({ everySeconds: 1 }, '2026-08-17T14:37:12Z', 2))
      .toEqual(['2026-08-17T14:37:13Z', '2026-08-17T14:37:14Z'])
  })
})

// ------------------------------------------------------------------ durate

describe('durate nella forma dell\'API', () => {
  it('legge ciò che il backend scrive', () => {
    expect(parseDurationSeconds('1s')).toBe(1)
    expect(parseDurationSeconds('10s')).toBe(10)
    expect(parseDurationSeconds('5m')).toBe(300)
    expect(parseDurationSeconds('1h')).toBe(3600)
    expect(parseDurationSeconds(' 30s ')).toBe(30)
  })

  it('non inventa un valore per ciò che non riconosce', () => {
    // `1.5h` il backend lo accetterebbe, ma `jobs.every_seconds` è un intero di
    // secondi: meglio nessun valore che uno troncato in silenzio.
    for (const raw of ['', '1.5h', '1h30m', 'abc', '10', '-5s']) {
      expect(parseDurationSeconds(raw), raw).toBeNull()
    }
  })

  it('riscrive nella forma che il backend rimanda indietro', () => {
    // Ciò che si legge dall'API si può rimandare indietro senza conversioni: è
    // la stessa scelta di `jobs.FormatDuration`.
    expect(formatDurationSeconds(1)).toBe('1s')
    expect(formatDurationSeconds(90)).toBe('90s')
    expect(formatDurationSeconds(300)).toBe('5m')
    expect(formatDurationSeconds(3600)).toBe('1h')
    expect(formatDurationSeconds(7200)).toBe('2h')
  })
})
