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

// Multilingua (SPEC §8-bis, R31–R32).
//
// Qui le asserzioni sono sui testi, contro la regola generale di questo file,
// perché il testo *è* il comportamento: «l'interfaccia è in tedesco» non si può
// verificare guardando la struttura della pagina. Sono cinque titoli, cambiano
// solo se cambia la pagina iniziale, e finché non cambia sono la sola prova che
// la traduzione arriva davvero fino allo schermo.
//
// La dashboard non ha rotte per lingua: il rilevamento non produce un redirect
// come sul sito pubblico, cambia lo stato dell'applicazione. Per questo si
// verifica ciò che l'utente vede — `<html lang>`, il titolo, il selettore — e
// non l'indirizzo, che resta lo stesso in tutte e cinque le lingue.

const SWITCHER = '[data-testid="locale-switcher"]'

/** Titolo della pagina iniziale in ciascuna lingua, da `apps/dashboard/content/`. */
const TITLES = {
  en: 'Overview',
  it: 'Panoramica',
  es: 'Resumen',
  de: 'Übersicht',
  fr: 'Aperçu',
} as const

test.describe('lingua', () => {
  test.describe('browser in tedesco', () => {
    test.use({ locale: 'de-DE' })

    test('al primo accesso l\'interfaccia segue il browser (R31)', async ({ page }) => {
      await page.goto('/')

      await expect(page.locator('h1')).toHaveText(TITLES.de)
      await expect(page.locator('html')).toHaveAttribute('lang', 'de')
      // Il selettore mostra la lingua in cui ci si trova, non la predefinita.
      await expect(page.locator(SWITCHER)).toHaveValue('de')
    })
  })

  test.describe('browser in una lingua non supportata', () => {
    test.use({ locale: 'ja-JP' })

    test('ripiega sull\'inglese (R31)', async ({ page }) => {
      await page.goto('/')

      await expect(page.locator('h1')).toHaveText(TITLES.en)
      await expect(page.locator('html')).toHaveAttribute('lang', 'en')
    })
  })

  test.describe('browser in italiano', () => {
    test.use({ locale: 'it-CH' })

    test('confronta il solo sottotag primario', async ({ page }) => {
      // `it-CH` è italiano: non abbiamo varianti regionali da distinguere, e un
      // confronto sul tag intero manderebbe in inglese mezza Svizzera.
      await page.goto('/')

      await expect(page.locator('h1')).toHaveText(TITLES.it)
    })
  })

  test.describe('selettore', () => {
    test.use({ locale: 'de-DE' })

    test('offre le cinque lingue e le applica tutte (R32)', async ({ page }) => {
      const errors = collectPageErrors(page)
      await page.goto('/')

      await expect(page.locator(`${SWITCHER} option`)).toHaveText([
        'English',
        'Italiano',
        'Español',
        'Deutsch',
        'Français',
      ])

      for (const [code, title] of Object.entries(TITLES)) {
        await page.selectOption(SWITCHER, code)

        await expect(page.locator('h1')).toHaveText(title)
        await expect(page.locator('html')).toHaveAttribute('lang', code)
        // Il titolo del documento segue la lingua: se `useHead` ricevesse un
        // oggetto statico resterebbe fermo a quella dell'avvio.
        await expect(page).toHaveTitle(`${title} · Postqron`)
      }

      await page.waitForLoadState('networkidle')
      expect(errors).toEqual([])
    })

    test('cambia lingua sul posto, senza navigare', async ({ page }) => {
      await page.goto('/')
      const before = page.url()

      // Un segno lasciato nel contesto JavaScript della pagina: sopravvive a un
      // cambio di stato, non a una navigazione né a un ricaricamento.
      await page.evaluate(() => {
        (window as unknown as Record<string, unknown>).__stillHere = true
      })

      await page.selectOption(SWITCHER, 'fr')
      await expect(page.locator('html')).toHaveAttribute('lang', 'fr')

      // È la differenza sostanziale dal sito pubblico, dove cambiare lingua
      // significa cambiare indirizzo. Qui la lingua non sta nell'indirizzo:
      // cambiarla non deve far perdere la schermata su cui si sta lavorando, né
      // lo stato che quella schermata ha in memoria.
      expect(page.url()).toBe(before)
      expect(
        await page.evaluate(() => (window as unknown as Record<string, unknown>).__stillHere),
      ).toBe(true)
    })

    test('la scelta prevale sul rilevamento e persiste fra le visite (R32)', async ({ page, context }) => {
      await page.goto('/')
      await expect(page.locator('h1')).toHaveText(TITLES.de)

      await page.selectOption(SWITCHER, 'it')
      await expect(page.locator('h1')).toHaveText(TITLES.it)

      // Ricaricando, e poi in una visita successiva con lo stesso browser: la
      // scelta vince sul tedesco che il browser continua a chiedere.
      await page.reload()
      await expect(page.locator('h1')).toHaveText(TITLES.it)

      const later = await context.newPage()
      await later.goto('/')
      await expect(later.locator('h1')).toHaveText(TITLES.it)
      await expect(later.locator(SWITCHER)).toHaveValue('it')
      await later.close()
    })

    test('un valore memorizzato non più valido non blocca l\'avvio', async ({ page }) => {
      // La chiave di `localStorage` sopravvive agli aggiornamenti: se un giorno
      // togliessimo una lingua, chi l'aveva scelta deve comunque poter entrare.
      await page.goto('/')
      await page.evaluate(() => window.localStorage.setItem('postqron:locale', 'pt'))
      await page.reload()

      await expect(page.locator('h1')).toHaveText(TITLES.de)
    })
  })

  test('il robots.txt non vieta la scansione, che leggerebbe il noindex', async ({ request }) => {
    // `robots.txt` governa la scansione, non l'indicizzazione. Un `Disallow: /`
    // qui impedirebbe al crawler di leggere il `noindex` del guscio, e un
    // indirizzo raggiunto da un link esterno finirebbe nell'indice come voce
    // senza contenuto: le due direttive si annullano, e a difendere la
    // dashboard è il `noindex`. Il test esiste perché la modifica «ovvia» — un
    // Disallow — la scoprirebbe soltanto Google.
    const response = await request.get('/robots.txt')
    expect(response.status()).toBe(200)
    expect(await response.text()).not.toMatch(/^Disallow:\s*\/\s*$/m)
  })

  test('non dichiara lingue alternative: non è roba da indicizzare', async ({ page }) => {
    // A differenza del sito pubblico qui non esistono `hreflang` né `canonical`:
    // le pagine stanno dietro autenticazione e nessun crawler deve trovarne
    // cinque varianti. Copiarli dal sito sarebbe lavoro da rimuovere.
    await page.goto('/')

    await expect(page.locator('meta[name="robots"]')).toHaveAttribute('content', /noindex/)
    await expect(page.locator('link[rel="alternate"]')).toHaveCount(0)
    await expect(page.locator('link[rel="canonical"]')).toHaveCount(0)
  })
})
