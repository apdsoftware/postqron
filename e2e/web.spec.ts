import { expect, test } from '@playwright/test'
import { collectPageErrors } from './support/page-errors'

// Sito pubblico — output di `nuxt generate` con ssr: true, quindi pre-renderizzato.
//
// Le asserzioni sono deliberatamente strutturali (esiste un h1, il titolo non è
// vuoto, la pagina si idrata) e non sui testi: il design system Hexagon arriva
// con la issue #401 e riscriverà i contenuti. Un test e2e che si rompe perché è
// cambiato un titolo insegna solo a disattivarlo.

test.describe('sito pubblico', () => {
  test('la home è pre-renderizzata: il contenuto è nell\'HTML servito, senza eseguire JS', async ({ request }) => {
    const response = await request.get('/')
    expect(response.status()).toBe(200)

    const html = await response.text()

    // Pre-rendering per la SEO (SPEC §2): senza queste due, `nuxt generate` ha
    // prodotto un guscio vuoto e il sito è invisibile ai crawler.
    expect(html).toMatch(/<html[^>]*\slang="it"/)
    expect(html).toMatch(/<h1[^>]*>[^<]/)
    expect(html).toMatch(/<title>[^<]+<\/title>/)
  })

  test('la home si apre in un browser senza errori', async ({ page }) => {
    const errors = collectPageErrors(page)

    await page.goto('/')
    await expect(page.locator('h1')).toHaveText(/\S/)
    await expect(page).toHaveTitle(/\S/)

    // L'idratazione è asincrona: senza attenderla, un errore che arriva subito
    // dopo il primo paint sfuggirebbe.
    await page.waitForLoadState('networkidle')

    expect(errors).toEqual([])
  })

  test('la build statica non lascia rotte servite da un runtime', async ({ request }) => {
    // Vincolo di distribuzione (SPEC §2): l'unica origin dinamica è il backend
    // Go. Se `nuxt generate` avesse prodotto endpoint Nitro, sarebbero sotto
    // /api/ e su Cloudflare Pages non risponderebbe nessuno.
    const response = await request.get('/api/_nuxt_island')
    expect(response.status()).toBe(404)
  })
})
