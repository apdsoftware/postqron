import { describe, expect, it } from 'vitest'

import { ApiError } from '../utils/api'
import {
  authErrorKey,
  isPublicPath,
  localizeNextPath,
  LOGIN_PATH,
  MIN_PASSWORD_LENGTH,
  REGISTER_PATH,
  safeNextPath,
} from '../utils/auth'

describe('isPublicPath', () => {
  it('riconosce le due schermate raggiungibili senza sessione', () => {
    expect(isPublicPath(LOGIN_PATH)).toBe(true)
    expect(isPublicPath(REGISTER_PATH)).toBe(true)
  })

  it('uno slash finale non cambia la schermata', () => {
    expect(isPublicPath('/login/')).toBe(true)
  })

  it('tutto il resto è protetto, compresa la radice', () => {
    expect(isPublicPath('/')).toBe(false)
    expect(isPublicPath('/jobs/42')).toBe(false)
    // Un percorso che *comincia* come uno pubblico non lo è: `/login-help`
    // sarebbe una schermata a sé, e aprirla a chi non ha sessione per via del
    // prefisso è il modo in cui una regola sui prefissi concede per sbaglio.
    expect(isPublicPath('/login-help')).toBe(false)
  })

  it('vale in tutte e cinque le lingue', () => {
    // Le rotte sono prefissate (SPEC §8-bis): l'indirizzo vero dell'accesso è
    // `/it/login`. Senza togliere il prefisso, la guardia lo tratterebbe da
    // schermata protetta e rimanderebbe all'accesso — cioè lì.
    expect(isPublicPath('/it/login')).toBe(true)
    expect(isPublicPath('/de/register/')).toBe(true)
    expect(isPublicPath('/fr/jobs/42')).toBe(false)
    // La radice di una lingua è la panoramica, non una schermata pubblica.
    expect(isPublicPath('/es')).toBe(false)
  })
})

describe('safeNextPath', () => {
  it('accetta un percorso interno, query compresa', () => {
    expect(safeNextPath('/jobs/42')).toBe('/jobs/42')
    expect(safeNextPath('/jobs?stato=fallito&pagina=2')).toBe('/jobs?stato=fallito&pagina=2')
  })

  it('non dichiara la panoramica nuda, che è già il ripiego', () => {
    // Altrimenti `?next=/` comparirebbe nell'indirizzo di chiunque apra la
    // dashboard da scollegato, senza cambiare dove si finisce.
    expect(safeNextPath('/')).toBeNull()
    // Con una query invece sì: i filtri di un elenco stanno lì, e tornare senza
    // di essi è comunque aver perso il posto.
    expect(safeNextPath('/?scheda=salute')).toBe('/?scheda=salute')
  })

  it('la panoramica nuda è tale anche col prefisso di lingua', () => {
    // `/it` *è* la panoramica: dichiararla come ritorno è lo stesso parametro
    // che non cambia niente, scritto in italiano.
    expect(safeNextPath('/it')).toBeNull()
    expect(safeNextPath('/de/')).toBeNull()
    // E con una query si conserva, come la sua gemella senza prefisso.
    expect(safeNextPath('/it?scheda=salute')).toBe('/it?scheda=salute')
  })

  it('accetta una rotta profonda prefissata, che è ciò che compone un\'email', () => {
    // È il valore che la guardia ci mette venendo da `/it/jobs/42`, ed è anche
    // la forma in cui il backend compone i link (`AppURL()`, R21+R33).
    expect(safeNextPath('/it/jobs/42')).toBe('/it/jobs/42')
    expect(safeNextPath('/de/jobs?stato=fallito')).toBe('/de/jobs?stato=fallito')
  })

  it('rifiuta un indirizzo assoluto', () => {
    expect(safeNextPath('https://phishing.example/')).toBeNull()
    expect(safeNextPath('javascript:alert(1)')).toBeNull()
  })

  it('rifiuta il protocollo relativo, che sembra un percorso e non lo è', () => {
    // È il caso che inganna l'occhio: comincia con uno slash come `/jobs`, ma
    // porta su un'altra origin — e ci porterebbe subito dopo che l'utente ha
    // scritto la password.
    expect(safeNextPath('//phishing.example/')).toBeNull()
    expect(safeNextPath('/\\phishing.example/')).toBeNull()
  })

  it('rifiuta i caratteri di controllo', () => {
    expect(safeNextPath('/jobs\n/altro')).toBeNull()
  })

  it('rifiuta le schermate dell\'autenticazione, che sarebbero un anello', () => {
    expect(safeNextPath(LOGIN_PATH)).toBeNull()
    expect(safeNextPath(REGISTER_PATH)).toBeNull()
    expect(safeNextPath('/login?next=/login')).toBeNull()
    // L'anello non si scioglie cambiando lingua.
    expect(safeNextPath('/it/login')).toBeNull()
    expect(safeNextPath('/de/register')).toBeNull()
  })

  it('rifiuta ciò che non è nemmeno una stringa', () => {
    // `route.query.next` è `string | string[] | null`: due `?next=` nella stessa
    // barra degli indirizzi danno un array, e nessuno lo scrive per sbaglio.
    expect(safeNextPath(undefined)).toBeNull()
    expect(safeNextPath(null)).toBeNull()
    expect(safeNextPath(['/jobs', '/altro'])).toBeNull()
    expect(safeNextPath('')).toBeNull()
  })
})

describe('localizeNextPath', () => {
  it('porta il ritorno nella lingua della pagina su cui si è', () => {
    // Chi è stato rimbalzato da `/en/jobs/42` e ha cambiato il selettore per
    // capire cosa gli si chiedeva non deve ritrovarsi la schermata in inglese
    // dopo aver scritto la password.
    expect(localizeNextPath('/en/jobs/42', 'it')).toBe('/it/jobs/42')
    expect(localizeNextPath('/it/jobs/42', 'it')).toBe('/it/jobs/42')
  })

  it('prefissa un ritorno che non dichiara una lingua', () => {
    // Non capita dalla guardia, che scrive sempre percorsi prefissati, ma
    // `?next=` si scrive anche a mano.
    expect(localizeNextPath('/jobs/42', 'de')).toBe('/de/jobs/42')
    expect(localizeNextPath('/', 'de')).toBe('/de')
  })

  it('non tocca il dove: query e ancora restano in coda', () => {
    // `next` dice quale schermata; i filtri e la posizione stanno lì dentro e
    // non vanno interpretati.
    expect(localizeNextPath('/en/jobs?stato=fallito&pagina=2', 'fr'))
      .toBe('/fr/jobs?stato=fallito&pagina=2')
    expect(localizeNextPath('/en/jobs/42#storico', 'es')).toBe('/es/jobs/42#storico')
    expect(localizeNextPath('/en?scheda=salute', 'it')).toBe('/it?scheda=salute')
  })
})

describe('authErrorKey', () => {
  const error = (status: number, code: string | null = null) =>
    ApiError.fromStatus('https://api.postqron.com/auth/login', status, '', { code })

  it('un 401 dice «email o password non corretti», e nient\'altro', () => {
    // La proprietà da non rompere: il backend non distingue fra utente
    // inesistente e password sbagliata — pareggia perfino i tempi di risposta —
    // e l'interfaccia non deve reintrodurre la distinzione.
    expect(authErrorKey(error(401, 'invalid_credentials'))).toBe('credentials')
  })

  it('un 429 non è «controlla i dati inseriti»', () => {
    // Ricadrebbe in `invalid` come tutti i 4xx restanti, e manderebbe a
    // ricontrollare una password che magari era giusta.
    expect(authErrorKey(error(429, 'rate_limited'))).toBe('tooManyAttempts')
  })

  it('un 403 è l\'account sospeso, e si può dire', () => {
    // Lo legge solo chi ha già indovinato la password: il backend risponde così
    // dopo averla verificata, quindi non aiuta nessuno a enumerare account.
    expect(authErrorKey(error(403, 'account_suspended'))).toBe('suspended')
  })

  it('distingue i due rifiuti della registrazione dal codice, non dal 400', () => {
    expect(authErrorKey(error(400, 'invalid_email'))).toBe('invalidEmail')
    expect(authErrorKey(error(400, 'weak_password'))).toBe('weakPassword')
  })

  it('raccoglie in un messaggio solo tutto ciò che non porta a un\'azione diversa', () => {
    expect(authErrorKey(error(500))).toBe('unexpected')
    expect(authErrorKey(error(400, 'qualcosa_di_nuovo'))).toBe('unexpected')
    expect(authErrorKey(ApiError.network('https://api.postqron.com/auth/login', new Error('boom'))))
      .toBe('unexpected')
  })
})

describe('MIN_PASSWORD_LENGTH', () => {
  it('non scende sotto il minimo del backend', () => {
    // Gemello di `password_test.go`: se un giorno qui comparisse un numero più
    // basso, il modulo prometterebbe una password che il backend rifiuta.
    expect(MIN_PASSWORD_LENGTH).toBeGreaterThanOrEqual(12)
  })
})
