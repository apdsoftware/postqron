import { expect, test } from '@playwright/test'
import { collectPageErrors } from './support/page-errors'

// Dashboard — output di `nuxt generate` con ssr: false, quindi una SPA statica.
//
// Rispetto al sito pubblico cambiano le garanzie da verificare: qui l'HTML
// servito è volutamente un guscio, e il contenuto deve comparire dopo che il
// browser ha eseguito il bundle. Le due cose vanno provate insieme — un guscio
// che resta vuoto è indistinguibile da una build riuscita se si guarda solo il
// codice HTTP.

test.describe('dashboard', () => {
  test('l\'HTML servito è un guscio: il contenuto arriva dal client', async ({ request, page }) => {
    const response = await request.get('/')
    expect(response.status()).toBe(200)

    const html = await response.text()
    // ssr: false — se qui comparisse già l'h1, qualcuno ha riacceso l'SSR e la
    // dashboard non è più distribuibile come file statici.
    expect(html).not.toMatch(/<h1[^>]*>/)

    // Lo stesso percorso, aperto da un browser, deve invece renderizzare.
    await page.goto('/')
    await expect(page.locator('h1')).toHaveText(/\S/)
  })

  test('la SPA si idrata senza errori', async ({ page }) => {
    const errors = collectPageErrors(page)

    await page.goto('/')
    await expect(page.locator('h1')).toBeVisible()
    await page.waitForLoadState('networkidle')

    expect(errors).toEqual([])
  })

  test('una rotta profonda ricade su index.html e la SPA la risolve', async ({ page }) => {
    // Regola `/* /index.html 200` di public/_redirects, riprodotta dal server
    // statico dei test. Senza, un refresh su /jobs/42 restituirebbe 404: è il
    // modo classico in cui una SPA statica si rompe solo in produzione, dove
    // nessuno ricarica mai la pagina durante lo sviluppo.
    const response = await page.goto('/jobs/42')
    expect(response?.status()).toBe(200)

    // Il router Vue ha preso il controllo: la pagina non è più il guscio vuoto.
    await expect(page.locator('#__nuxt')).toHaveText(/\S/)
  })
})
