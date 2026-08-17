import type { Page } from '@playwright/test'

/**
 * Raccoglie tutto ciò che il browser considera un guasto: eccezioni non gestite,
 * `console.error` e richieste di rete fallite.
 *
 * Serve perché la maggior parte dei modi in cui una build statica si rompe non
 * cambia il DOM: un asset con base URL sbagliata, un chunk che non si carica,
 * un errore di idratazione. La pagina resta in piedi e il test passerebbe.
 *
 * Va agganciato prima della navigazione.
 */
export function collectPageErrors(page: Page): string[] {
  const errors: string[] = []

  page.on('pageerror', error => errors.push(`pageerror: ${error.message}`))

  page.on('console', message => {
    if (message.type() === 'error') errors.push(`console.error: ${message.text()}`)
  })

  page.on('requestfailed', request => {
    errors.push(`request fallita: ${request.url()} (${request.failure()?.errorText ?? 'motivo ignoto'})`)
  })

  page.on('response', response => {
    if (response.status() >= 400) {
      errors.push(`risposta ${response.status()}: ${response.url()}`)
    }
  })

  return errors
}
