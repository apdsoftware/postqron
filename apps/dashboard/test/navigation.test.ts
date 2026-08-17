import { describe, expect, it } from 'vitest'

import { dashboardContent } from '~/content'
import { ICONS } from '~/utils/icons'
import { isActivePath, NAV_IDS, NAVIGATION } from '~/utils/navigation'
import { LOCALE_CODES } from '~/utils/locale'

/**
 * Il registro della navigazione è il contratto fra questa issue e la dozzina
 * che seguono: ognuna aggiunge una sezione, e ciò che si verifica qui è che
 * aggiungerla male si veda subito invece che a pagina aperta.
 */
describe('registro della navigazione', () => {
  it('dichiara una voce per ogni sezione, e nessuna in più', () => {
    expect(NAVIGATION.map(entry => entry.id).sort()).toEqual([...NAV_IDS].sort())
  })

  it('non ha percorsi né identificatori ripetuti', () => {
    // Due voci sullo stesso percorso si evidenzierebbero insieme, e nessuna
    // delle due sarebbe sbagliata: è il tipo di difetto che si guarda senza
    // vederlo.
    expect(new Set(NAVIGATION.map(entry => entry.path)).size).toBe(NAVIGATION.length)
    expect(new Set(NAVIGATION.map(entry => entry.id)).size).toBe(NAVIGATION.length)
  })

  it('usa percorsi assoluti', () => {
    // Un percorso relativo si risolverebbe rispetto alla pagina corrente: la
    // stessa voce porterebbe altrove a seconda di dove la si preme.
    for (const entry of NAVIGATION) expect(entry.path.startsWith('/')).toBe(true)
  })

  it('usa icone che esistono nel registro', () => {
    for (const entry of NAVIGATION) expect(ICONS).toHaveProperty(entry.icon)
  })

  it.each(LOCALE_CODES)('ha il nome della sezione in %s', (locale) => {
    // I tipi lo impongono già in compilazione; qui si verifica che nessuno
    // l'abbia soddisfatto con una stringa vuota, che compila e non si legge.
    for (const entry of NAVIGATION) {
      expect(dashboardContent[locale].shell.nav[entry.id].trim()).not.toBe('')
    }
  })
})

describe('isActivePath', () => {
  it('riconosce la pagina esatta', () => {
    expect(isActivePath('/jobs', '/jobs')).toBe(true)
  })

  it('resta attiva sulle pagine di dettaglio della sezione', () => {
    // Chi guarda un job sta in «Cronjob»: una barra laterale che non evidenzia
    // niente gli fa perdere il punto in cui si trova.
    expect(isActivePath('/jobs', '/jobs/42')).toBe(true)
    expect(isActivePath('/jobs', '/jobs/42/runs')).toBe(true)
  })

  it('non confonde due sezioni che iniziano allo stesso modo', () => {
    // Il confronto è per segmenti interi: senza, `/jobs` risulterebbe attiva
    // anche su `/jobs-archive`, e le due voci si accenderebbero insieme.
    expect(isActivePath('/jobs', '/jobs-archive')).toBe(false)
    expect(isActivePath('/job', '/jobs')).toBe(false)
  })

  it('la radice vale solo esatta', () => {
    // Come prefisso corrisponderebbe a tutto, e la panoramica risulterebbe
    // attiva su ogni schermata della dashboard.
    expect(isActivePath('/', '/')).toBe(true)
    expect(isActivePath('/', '/jobs')).toBe(false)
    expect(isActivePath('/', '/jobs/42')).toBe(false)
  })

  it('ignora lo slash finale', () => {
    // `/jobs` e `/jobs/` sono la stessa schermata: quale dei due arrivi da
    // `useRoute().path` dipende da come ci si è arrivati, non da dove si è.
    expect(isActivePath('/jobs', '/jobs/')).toBe(true)
    expect(isActivePath('/jobs/', '/jobs')).toBe(true)
    expect(isActivePath('/', '')).toBe(true)
  })
})
