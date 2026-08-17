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

  test('i dati strutturati arrivano nell\'HTML servito e sono JSON valido', async ({ request }) => {
    // Il contenuto del grafo è verificato dai test unitari; qui interessa che
    // il pre-rendering lo abbia scritto davvero e che sia rileggibile — un
    // JSON-LD che un motore non riesce a parsare è come non averlo.
    for (const path of ['/en/', '/en/pricing/', '/en/faq/', '/en/contact/']) {
      const response = await request.get(path)
      const html = await response.text()

      const block = html.match(
        /<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/,
      )
      expect(block, `${path} non ha dati strutturati`).not.toBeNull()

      const graph = JSON.parse(block![1]!) as { '@context': string, '@graph': unknown[] }
      expect(graph['@context']).toBe('https://schema.org')
      expect(graph['@graph'].length).toBeGreaterThan(0)
    }
  })
})

test.describe('peso delle immagini (R53-bis)', () => {
  // Un telefono di fascia media: 390 px logici, due pixel fisici per uno. È la
  // combinazione che decide quale variante il browser sceglie dal `srcset`, e
  // fissarla qui è ciò che rende il numero sotto confrontabile fra due
  // esecuzioni. Con il viewport del progetto (desktop, densità 1) la stessa
  // pagina scaricherebbe varianti diverse e la soglia non direbbe niente.
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true })

  /**
   * Tetto per le immagini dell'intera home, scorrimento fino in fondo compreso.
   *
   * Misurato il 2026-08-17 su questa build: 77 KB, contro i 524 KB da cui
   * partiva la issue #502. Il margine lascia spazio a una fotografia in più
   * senza toccare il numero, e non a una fotografia dimenticata non ottimizzata:
   * un solo JPEG del vecchio calibro lo sfonderebbe da solo.
   */
  const IMAGE_BUDGET = 100 * 1024

  test('la home non supera il tetto di peso delle immagini', async ({ page }) => {
    // I corpi si leggono in modo asincrono: raccogliere le promesse e attenderle
    // tutte alla fine è ciò che rende il totale ripetibile. Sommare dentro il
    // gestore lasciava fuori le risposte ancora in lettura quando il test
    // arrivava all'asserzione, e il numero cambiava a ogni esecuzione.
    const bodies: Promise<Buffer>[] = []
    page.on('response', (response) => {
      if (response.request().resourceType() === 'image') bodies.push(response.body())
    })

    await page.goto('/it/')
    await page.waitForLoadState('networkidle')

    // Le immagini sotto la piega sono differite: senza scorrere non partono, e
    // un tetto che non le contasse premierebbe proprio ciò che va misurato. Lo
    // scorrimento ricalcola l'altezza a ogni passo, perché caricando le
    // copertine la pagina si allunga.
    await page.evaluate(async () => {
      for (let y = 0; y < document.documentElement.scrollHeight; y += 400) {
        window.scrollTo(0, y)
        await new Promise(resolve => setTimeout(resolve, 100))
      }
    })

    // Il conteggio vale solo a scarico finito: `networkidle` da solo tornerebbe
    // mentre l'ultima copertina è ancora in volo.
    const total = await page.locator('img').count()
    await expect.poll(() => page.evaluate(() => Array.from(document.images).filter(image => image.complete).length))
      .toBe(total)

    const bytes = (await Promise.all(bodies)).reduce((sum, body) => sum + body.length, 0)

    expect(bodies.length, 'nessuna immagine scaricata: la misura non proverebbe niente').toBeGreaterThan(0)
    expect(bytes, `immagini della home: ${Math.round(bytes / 1024)} KB`).toBeLessThanOrEqual(IMAGE_BUDGET)
  })

  test('l\'elemento LCP arriva in formato moderno e non è differito', async ({ page }) => {
    await page.goto('/it/')

    const hero = page.locator('#welcome img').first()

    // `currentSrc` e non `src`: è la variante che il browser ha davvero scelto
    // fra quelle del `<picture>`, l'unica prova che l'AVIF è arrivato.
    await expect(hero).toHaveAttribute('fetchpriority', 'high')
    await expect.poll(() => hero.evaluate((img: HTMLImageElement) => img.currentSrc))
      .toMatch(/\.avif$/)

    // `loading="lazy"` sull'elemento LCP è il verso sbagliato: ritarderebbe la
    // sola immagine che conta per la metrica.
    expect(await hero.getAttribute('loading')).toBeNull()
  })
})

test.describe('markup delle immagini servite', () => {
  test('ogni `<img>` dell\'HTML pre-renderizzato dichiara larghezza e altezza', async ({ request }) => {
    // Il gemello di questo controllo sta in apps/web/test/images.test.ts e legge
    // i sorgenti `.vue`. Qui si guarda ciò che il browser riceve davvero: un
    // componente può dichiarare gli attributi e perderli per strada.
    const html = await (await request.get('/it/')).text()
    const tags = html.match(/<img\b[^>]*>/g) ?? []

    expect(tags.length).toBeGreaterThan(0)
    expect(tags.filter(tag => !/\swidth="/.test(tag) || !/\sheight="/.test(tag))).toEqual([])
  })

  test('il precarico dell\'hero indica esattamente ciò che il `<picture>` sceglierà', async ({ request }) => {
    // Se `imagesrcset` e `imagesizes` divergessero da quelli del `<source>`
    // AVIF, il browser scaricherebbe due immagini invece di una: un precarico
    // sbagliato costa più di nessun precarico.
    const html = await (await request.get('/it/')).text()

    const preload = /<link rel="preload"[^>]*as="image"[^>]*>/.exec(html)?.[0]
    expect(preload).toBeDefined()
    expect(preload).toContain('type="image/avif"')

    const preloadSrcset = /imagesrcset="([^"]*)"/.exec(preload!)?.[1]
    const preloadSizes = /imagesizes="([^"]*)"/.exec(preload!)?.[1]
    const source = /<source type="image\/avif"[^>]*>/.exec(html)?.[0]

    expect(source).toContain(`srcset="${preloadSrcset}"`)
    expect(source).toContain(`sizes="${preloadSizes}"`)
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
