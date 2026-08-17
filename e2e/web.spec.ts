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

test.describe('reperibilità (R53-ter)', () => {
  // `robots.txt` e `sitemap.xml` devono essere **file** nell'output statico: in
  // produzione non gira alcun Nitro (SPEC §2) e una rotta non risponderebbe a
  // nessuno. È una proprietà del sito servito, non del codice, e i test unitari
  // di `apps/web/test/sitemap.test.ts` non possono vederla.

  test('robots.txt è un file servito e rimanda alla sitemap', async ({ request }) => {
    const response = await request.get('/robots.txt')
    expect(response.status()).toBe(200)

    const body = await response.text()
    expect(body).toContain('User-agent: *')

    // L'indirizzo della sitemap è assoluto — il formato non ammette percorsi
    // relativi — e l'origin è quella della build, non quella del server di test.
    const sitemap = body.match(/^Sitemap: (\S+)$/m)
    expect(sitemap).not.toBeNull()
    expect(new URL(sitemap![1]!).pathname).toBe('/sitemap.xml')
  })

  test('sitemap.xml è XML valido e non contiene un solo 404', async ({ page, request }) => {
    const response = await request.get('/sitemap.xml')
    expect(response.status()).toBe(200)

    const xml = await response.text()

    // Il parser è quello del browser, non un'espressione regolare: se l'XML è
    // malformato `parseFromString` restituisce un documento `<parsererror>`
    // invece di lanciare, ed è l'unico modo di accorgersene.
    const parsed = await page.evaluate((source) => {
      const document = new DOMParser().parseFromString(source, 'application/xml')
      const error = document.querySelector('parsererror')

      return {
        error: error?.textContent ?? null,
        root: document.documentElement.tagName,
        locations: Array.from(document.querySelectorAll('loc'), loc => loc.textContent!),
        alternates: Array.from(document.querySelectorAll('url'), url =>
          Array.from(url.getElementsByTagName('xhtml:link'), link => link.getAttribute('hreflang')),
        ),
      }
    }, xml)

    expect(parsed.error).toBeNull()
    expect(parsed.root).toBe('urlset')

    // Tutte le pagine in tutte e cinque le lingue (SPEC §8-bis): una sitemap
    // che elenca solo l'inglese dice a un motore che le altre quattro non
    // esistono.
    expect(parsed.locations.length).toBeGreaterThan(0)
    for (const locale of LOCALES) {
      const localized = parsed.locations.filter(loc => new URL(loc).pathname.startsWith(`/${locale}/`))
      expect(localized.length).toBe(parsed.locations.length / LOCALES.length)
    }

    // Ogni voce dichiara le cinque traduzioni più `x-default`.
    for (const group of parsed.alternates) {
      expect(group).toEqual([...LOCALES, 'x-default'])
    }

    // Il controllo che rende la sitemap utile invece che dannosa. L'origin è
    // quella della build: qui conta il percorso, che è ciò che il sito serve.
    for (const location of parsed.locations) {
      const listed = await request.get(new URL(location).pathname)
      expect(listed.status(), `${location} non risponde 200`).toBe(200)
    }
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
