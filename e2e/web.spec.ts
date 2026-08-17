import { expect, test } from '@playwright/test'
import { collectPageErrors } from './support/page-errors'

// Sito pubblico — output di `nuxt generate` con ssr: true, quindi pre-renderizzato.
//
// Le asserzioni sono deliberatamente strutturali (esiste un h1, il titolo non è
// vuoto, la pagina si idrata) e non sui testi: i contenuti cambiano, e un test
// e2e che si rompe perché è cambiato un titolo insegna solo a disattivarlo.
//
// L'eccezione è il multilingua (SPEC §8-bis): lì il comportamento *è*
// l'indirizzo — quale lingua risponde a quale percorso, dove porta la radice,
// cosa dichiarano `canonical` e `hreflang` — e non si può verificare senza
// servire l'output statico e aprirlo in un browser.

const LOCALES = ['en', 'it', 'es', 'de', 'fr'] as const

test.describe('sito pubblico', () => {
  for (const locale of LOCALES) {
    test(`/${locale}/ è pre-renderizzata: il contenuto è nell'HTML servito, senza eseguire JS`, async ({ request }) => {
      const response = await request.get(`/${locale}/`)
      expect(response.status()).toBe(200)

      const html = await response.text()

      // Pre-rendering per la SEO (SPEC §2): senza queste, `nuxt generate` ha
      // prodotto un guscio vuoto e il sito è invisibile ai crawler.
      expect(html).toMatch(new RegExp(`<html[^>]*\\slang="${locale}"`))
      expect(html).toMatch(/<h1[^>]*>[^<]/)
      expect(html).toMatch(/<title>[^<]+<\/title>/)

      // Ogni versione dichiara il proprio indirizzo canonico e le altre quattro
      // come traduzioni, sé compresa, più `x-default` sull'inglese. Senza, le
      // cinque versioni competono fra loro nei motori di ricerca.
      expect(html).toContain(`<link rel="canonical" href="http://localhost:3000/${locale}/">`)
      for (const other of LOCALES) {
        expect(html).toContain(`<link rel="alternate" hreflang="${other}" href="http://localhost:3000/${other}/">`)
      }
      expect(html).toContain('<link rel="alternate" hreflang="x-default" href="http://localhost:3000/en/">')
    })
  }

  test('la radice non ha contenuto proprio: smista e basta', async ({ request }) => {
    const response = await request.get('/')
    expect(response.status()).toBe(200)

    const html = await response.text()

    // Se la radice avesse una home propria diventerebbe una sesta versione da
    // tradurre e tenere allineata alle altre cinque (SPEC §8-bis).
    expect(html).not.toMatch(/<h1[^>]*>[^<]/)

    // Senza JavaScript restano i cinque link: sono l'unica via d'uscita.
    for (const locale of LOCALES) {
      expect(html).toContain(`href="/${locale}/"`)
    }
  })

  test('la home si apre in un browser senza errori', async ({ page }) => {
    const errors = collectPageErrors(page)

    await page.goto('/en/')
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

test.describe('lingua', () => {
  test.describe('browser in tedesco', () => {
    test.use({ locale: 'de-DE' })

    test('la radice smista sulla lingua del browser (R31)', async ({ page }) => {
      await page.goto('/')
      await page.waitForURL(/\/de\/$/)
      await expect(page.locator('html')).toHaveAttribute('lang', 'de')
    })
  })

  test.describe('browser in una lingua non supportata', () => {
    test.use({ locale: 'ja-JP' })

    test('la radice ripiega sull\'inglese', async ({ page }) => {
      await page.goto('/')
      await page.waitForURL(/\/en\/$/)
      await expect(page.locator('html')).toHaveAttribute('lang', 'en')
    })
  })

  test.describe('scelta esplicita', () => {
    test.use({ locale: 'de-DE' })

    test('il selettore prevale sul rilevamento e persiste fra le visite (R32)', async ({ page }) => {
      const errors = collectPageErrors(page)

      await page.goto('/')
      await page.waitForURL(/\/de\/$/)

      // Sopra i 992px la tendina si apre al passaggio del mouse.
      await page.locator('.site-header__menu > li.has-submenu').last().hover()
      await page.getByRole('link', { name: 'Italiano' }).click()

      await page.waitForURL(/\/it\/$/)
      await expect(page.locator('html')).toHaveAttribute('lang', 'it')
      await expect(page.locator('link[rel="canonical"]')).toHaveAttribute(
        'href',
        'http://localhost:3000/it/',
      )

      // Seconda visita, stesso browser: la scelta vince sul tedesco del browser.
      await page.goto('/')
      await page.waitForURL(/\/it\/$/)

      await page.waitForLoadState('networkidle')
      expect(errors).toEqual([])
    })
  })
})
