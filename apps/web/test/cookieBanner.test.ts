import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { beforeEach, describe, expect, it } from 'vitest'

import CookieBanner from '~/components/layout/CookieBanner.vue'
import { siteContent } from '~/content'
import { COOKIE_CONSENT_NAME, readCookieConsent, resetCookieConsentGate } from '~/utils/cookieConsent'
import { LOCALE_CODES } from '~/utils/locale'

/**
 * «Refusing is exactly as easy as accepting» (cookie policy §3) è la frase su
 * cui i garanti europei sanzionano, ed è quella che un restyling disfa senza
 * accorgersene: basta che il pulsante che accetta diventi pieno e quello che
 * rifiuta resti un bordo.
 *
 * Il test non guarda i colori — in jsdom non esiste un layout da misurare — ma
 * dimostra qualcosa di più forte: i due pulsanti sono indistinguibili per il
 * CSS. Stesso genitore, stessi attributi, stesse classi, nessuno stile inline;
 * e nel foglio di stile del componente nessuna regola che possa colpirne uno
 * solo. Se una regola non può distinguerli, nessuna resa può renderli diversi.
 */

const SOURCE = readFileSync(
  resolve(process.cwd(), 'components/layout/CookieBanner.vue'),
  'utf8',
)

/** Blocco `<style>` del componente, che è dove la parità si perderebbe. */
function styleBlock(): string {
  const start = SOURCE.indexOf('<style')
  const end = SOURCE.lastIndexOf('</style>')
  expect(start, 'il componente non ha un blocco <style>').toBeGreaterThan(-1)

  return SOURCE.slice(SOURCE.indexOf('>', start) + 1, end)
}

/** I selettori del foglio, senza i corpi delle regole. */
function selectors(): string[] {
  return styleBlock()
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('}')
    .map(rule => rule.split('{')[0]!.trim())
    .filter(Boolean)
    .flatMap(rule => rule.split(','))
    .map(selector => selector.trim())
    .filter(selector => selector && !selector.startsWith('@'))
}

/*
 * Il primo `mountSuspended` costruisce l'applicazione Nuxt — router, plugin,
 * componenti globali — e da solo supera i cinque secondi predefiniti di Vitest.
 * I successivi sono immediati. Il tetto vale per tutto il gruppo perché l'ordine
 * dei test non è una garanzia: a pagare il primo mount può toccare a chiunque.
 */
describe('banner cookie: rifiutare è facile quanto accettare', { timeout: 30_000 }, () => {
  beforeEach(() => {
    resetCookieConsentGate()
    document.cookie = `${COOKIE_CONSENT_NAME}=; Path=/; Max-Age=0`
  })

  it('offre due sole scelte, allo stesso livello e sotto lo stesso genitore', async () => {
    const banner = await mountSuspended(CookieBanner)
    const buttons = banner.findAll('button')

    // Due pulsanti e basta: nessun «Personalizza» dietro cui nascondere il
    // rifiuto, che è il modo consueto di rendere il rifiuto più caro.
    expect(buttons).toHaveLength(2)
    expect(buttons[0]!.element.parentElement).toBe(buttons[1]!.element.parentElement)
  })

  it('presenta i due pulsanti in modo indistinguibile per il CSS', async () => {
    const banner = await mountSuspended(CookieBanner)
    const [first, second] = banner.findAll('button').map(button => button.element)

    const attributesOf = (element: Element) =>
      Object.fromEntries([...element.attributes].map(attribute => [attribute.name, attribute.value]))

    expect(first!.tagName).toBe(second!.tagName)
    expect(attributesOf(first!)).toEqual(attributesOf(second!))
    expect(first!.getAttribute('style')).toBeNull()
    expect(second!.getAttribute('style')).toBeNull()
  })

  /*
   * Attributi identici lasciano aperta una sola porta: i selettori posizionali,
   * con cui il CSS può colpire il primo o il secondo figlio senza che nel
   * markup si veda nulla. Chiusa quella, la parità è una proprietà del
   * componente e non una scelta di stile che il prossimo restyling disfa.
   */
  it('non lascia al foglio di stile un modo per distinguerli', () => {
    const positional = /:(?:first|last|only|nth)-(?:child|of-type)|:not\(/

    const distinguishing = selectors().filter(
      selector => selector.includes('cookie-banner__choice') && positional.test(selector),
    )

    expect(distinguishing).toEqual([])
  })

  it.each([
    ['reject', false],
    ['accept', true],
  ] as const)('registra la scelta «%s» con un clic solo', async (choice, nonEssential) => {
    const banner = await mountSuspended(CookieBanner)
    const label = siteContent.en.cookieBanner[choice]
    const button = banner.findAll('button').find(candidate => candidate.text() === label)

    expect(button, `nessun pulsante con l'etichetta ${label}`).toBeTruthy()
    await button!.trigger('click')

    // Un clic: la scelta è registrata e il banner è sparito, senza conferme.
    expect(readCookieConsent(document.cookie)?.nonEssential).toBe(nonEssential)
    expect(banner.find('.cookie-banner').exists()).toBe(false)
  })

  it.each(LOCALE_CODES)('%s ha entrambe le etichette, e sono due frasi diverse', (locale) => {
    const { accept, reject } = siteContent[locale].cookieBanner

    expect(accept.trim()).toBeTruthy()
    expect(reject.trim()).toBeTruthy()
    expect(accept).not.toBe(reject)
  })
})
