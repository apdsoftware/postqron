import { readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from '@playwright/test'
import { HEALTHY, mockBackend } from './support/dashboard-api'
import { collectPageErrors } from './support/page-errors'

// Dashboard — output di `nuxt generate` con ssr: false, quindi una SPA statica.
//
// Rispetto al sito pubblico cambiano le garanzie da verificare: qui l'HTML
// servito è volutamente un guscio, e il contenuto deve comparire dopo che il
// browser ha eseguito il bundle. Le due cose vanno provate insieme — un guscio
// che resta vuoto è indistinguibile da una build riuscita se si guarda solo il
// codice HTTP.

/**
 * Fa rispondere il backend senza un backend acceso.
 *
 * Sono due le richieste che partono comunque, e nessuna delle due appartiene al
 * test che le subisce: la panoramica interroga l'health check appena si apre — è
 * ciò che deve fare, e `useApiResource()` parte al montaggio — e la guardia di
 * rotta chiede `/auth/session` prima ancora che l'applicazione si monti. Qui non
 * gira nessun backend Go: senza queste risposte ogni test finirebbe per misurare
 * il loro fallimento invece di ciò che vuole misurare.
 *
 * La sessione è valida perché è la condizione normale della dashboard: tutto
 * quello che c'è in questo file — il guscio, la navigazione, gli stati, il
 * tema — si guarda da collegati. Il caso opposto, e tutto ciò che discende
 * dall'autenticazione, sta in `dashboard-auth.spec.ts`.
 *
 * Chi vuole provare proprio il guasto sovrascrive la rotta con un `page.route()`
 * suo, che ha la precedenza sull'ultimo registrato.
 */
test.beforeEach(async ({ page }) => {
  await mockBackend(page, true)
})

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
      await mockBackend(later, true)
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

// Guscio Flowbite (SPEC §4.2): barra superiore, barra laterale, area del
// contenuto.
//
// Le asserzioni sono sulla struttura e sui ruoli, non sulle classi Tailwind: una
// classe cambiata è un ritocco grafico, una barra laterale sparita è un guasto,
// e un test che confonde i due insegna solo a disattivarlo. L'eccezione è la
// voce corrente, dove ciò che conta è proprio `aria-current`: senza, chi non
// vede lo sfondo grigio non sa in che sezione si trova (R54).

const SIDEBAR = '#sidebar'
const NAV_TOGGLE = '[data-testid="navigation-toggle"]'

test.describe('guscio', () => {
  test('barra superiore, navigazione e contenuto sono tre punti di riferimento distinti', async ({ page }) => {
    await page.goto('/')

    // La barra superiore contiene il marchio e i comandi globali.
    await expect(page.getByRole('navigation').first()).toBeVisible()
    // La barra laterale è la navigazione fra le sezioni.
    await expect(page.locator(SIDEBAR)).toBeVisible()
    // Il contenuto è dove le pagine scrivono, ed è l'unico `<main>`.
    await expect(page.locator('main#main-content')).toHaveCount(1)
  })

  test('la barra laterale elenca le sezioni e segna quella corrente', async ({ page }) => {
    await page.goto('/')

    const links = page.locator(`${SIDEBAR} nav a`)
    await expect(links).not.toHaveCount(0)

    // Sulla radice è attiva la panoramica, e lo dice a chi ascolta la pagina.
    await expect(page.locator(`${SIDEBAR} nav a[href="/"]`)).toHaveAttribute('aria-current', 'page')
  })

  test('su un indirizzo fuori dalle sezioni nessuna voce risulta attiva', async ({ page }) => {
    // La radice è un prefisso di qualunque percorso: senza il caso speciale in
    // `isActivePath()` la panoramica sarebbe attiva ovunque, e l'evidenziazione
    // smetterebbe di dire dove si è.
    await page.goto('/nessuna-sezione/42')

    await expect(page.locator(`${SIDEBAR} nav a[aria-current="page"]`)).toHaveCount(0)
  })

  test('il salto al contenuto è il primo comando raggiungibile da tastiera (R54)', async ({ page }) => {
    await page.goto('/')
    await expect(page.locator('h1')).toBeVisible()

    // Primo tabulatore dopo il caricamento: deve essere il salto, e deve
    // diventare visibile — un link che resta in `sr-only` col fuoco sopra c'è
    // per i lettori di schermo e non per chi vede e naviga da tastiera.
    await page.keyboard.press('Tab')
    const skip = page.locator('a[href="#main-content"]')
    await expect(skip).toBeFocused()
    await expect(skip).toBeVisible()

    // E deve portare davvero il fuoco nel contenuto: senza `tabindex="-1"` sul
    // `<main>` sposterebbe solo la finestra, e il tabulatore successivo
    // ripartirebbe dalla barra superiore, cioè da capo.
    await page.keyboard.press('Enter')
    await expect(page.locator('main#main-content')).toBeFocused()
  })
})

test.describe('cassetto della navigazione sul telefono', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('si apre, si chiude in tre modi, e lo dichiara', async ({ page }) => {
    await page.goto('/')

    const toggle = page.locator(NAV_TOGGLE)
    const sidebar = page.locator(SIDEBAR)

    // Sotto i 1024 px la barra laterale è chiusa e il pulsante lo dichiara: nel
    // template `aria-expanded` è scritto fisso a `true` e non cambia mai.
    await expect(sidebar).toBeHidden()
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')

    await toggle.click()
    await expect(sidebar).toBeVisible()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')

    // 1. Esc — è ciò che rende il pannello chiudibile da tastiera (WCAG 2.2 2.1.2).
    await page.keyboard.press('Escape')
    await expect(sidebar).toBeHidden()

    // 2. Un tocco fuori dal pannello, sul velo.
    await toggle.click()
    await expect(sidebar).toBeVisible()
    await page.mouse.click(370, 700)
    await expect(sidebar).toBeHidden()
  })

  test('scegliere una sezione richiude il cassetto', async ({ page }) => {
    // Senza, la pagina cambierebbe dietro un pannello che copre lo schermo: chi
    // ha premuto vede lo stesso pannello di prima e conclude che non ha funzionato.
    await page.goto('/nessuna-sezione/42')

    await page.locator(NAV_TOGGLE).click()
    await expect(page.locator(SIDEBAR)).toBeVisible()

    await page.locator(`${SIDEBAR} nav a[href="/"]`).click()

    await expect(page.locator(SIDEBAR)).toBeHidden()
    await expect(page.locator(`${SIDEBAR} nav a[href="/"]`)).toHaveAttribute('aria-current', 'page')
  })
})

// Indirizzo inesistente.
//
// La dashboard è una SPA statica: `_redirects` fa servire `index.html` per
// qualunque percorso, quindi il server non dà mai 404 — ed è voluto, perché un
// aggiornamento di pagina su `/jobs/42` deve funzionare. La conseguenza è che
// l'unico posto in cui si può dire «questo indirizzo non esiste» è il router
// lato client, e senza quella pagina un refuso darebbe il guscio con l'area del
// contenuto vuota: che si legge come un guasto, non come un indirizzo sbagliato.

test.describe('pagina non trovata', () => {
  test('un indirizzo che non esiste lo dice, dentro il guscio', async ({ page }) => {
    await page.goto('/questa-non-esiste')

    await expect(page.locator('h1')).toHaveText('Page not found')
    // Dentro il guscio: chi ci finisce deve poter andare altrove.
    await expect(page.locator(SIDEBAR)).toBeVisible()

    await page.getByRole('link', { name: 'Back to the overview' }).click()
    await expect(page).toHaveURL('/')
    await expect(page.locator('h1')).toHaveText('Overview')
  })
})

// Stati di caricamento, errore e vuoto (R56).
//
// Sono la ragione per cui `<AsyncState>` esiste: una vista che non li dichiara
// non sembra rotta, sembra vuota. Qui si verifica che i tre esiti arrivino
// davvero fino allo schermo, perché è l'unica cosa che i test unitari non
// possono vedere.

test.describe('stati di una vista (R56)', () => {
  test('il caricamento si vede, e poi lascia il posto al dato', async ({ page }) => {
    let release = () => {}
    const held = new Promise<void>((resolve) => { release = resolve })

    await page.route('**/healthz', async (route) => {
      await held
      await route.fulfill({ json: HEALTHY })
    })

    await page.goto('/')

    await expect(page.locator('[data-testid="state-loading"]')).toBeVisible()
    release()

    await expect(page.locator('[data-testid="health"]')).toBeVisible()
    await expect(page.locator('[data-testid="state-loading"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="health"]')).toContainText(HEALTHY.version)
  })

  test('un guasto del backend si dichiara, e «riprova» funziona davvero', async ({ page }) => {
    let broken = true
    await page.route('**/healthz', (route) => {
      if (broken) return route.fulfill({ status: 503, body: '' })
      return route.fulfill({ json: HEALTHY })
    })

    await page.goto('/')

    const error = page.locator('[data-testid="state-error"]')
    await expect(error).toBeVisible()
    // `role="alert"`: l'errore va annunciato appena compare, non solo scorrendo.
    await expect(error).toHaveAttribute('role', 'alert')
    // Il messaggio è tradotto, non quello dell'eccezione: un 503 non deve
    // portare in pagina una frase in inglese in mezzo a cinque lingue.
    await expect(error).toContainText('The backend ran into a problem')

    broken = false
    await page.locator('[data-testid="state-retry"]').click()

    await expect(page.locator('[data-testid="health"]')).toBeVisible()
    await expect(error).toHaveCount(0)
  })

  test('dove riprovare non serve, il pulsante non c\'è', async ({ page }) => {
    // Un 403 dà lo stesso esito all'infinito: offrire «riprova» inviterebbe a
    // premerlo e a concludere che l'applicazione è rotta, invece che che la
    // risposta è quella.
    await page.route('**/healthz', route => route.fulfill({ status: 403, body: '' }))

    await page.goto('/')

    await expect(page.locator('[data-testid="state-error"]')).toBeVisible()
    await expect(page.locator('[data-testid="state-retry"]')).toHaveCount(0)
  })
})

// Tema chiaro e scuro.
//
// Il template Flowbite è disegnato in due temi e ogni sua classe ha una variante
// `dark:`. Verificare che l'interruttore funzioni non è verificare una comodità:
// è ciò che impedisce di accumulare componenti senza varianti `dark:` finché
// riaccenderlo costa una revisione di tutto.

test.describe('tema', () => {
  test('l\'interruttore cambia tema e la scelta sopravvive alla visita', async ({ page, context }) => {
    await page.goto('/')

    const html = page.locator('html')
    await expect(html).not.toHaveClass(/\bdark\b/)

    await page.locator('[data-testid="theme-toggle"]').click()
    await expect(html).toHaveClass(/\bdark\b/)

    await page.reload()
    await expect(html).toHaveClass(/\bdark\b/)

    const later = await context.newPage()
    await mockBackend(later, true)
    await later.goto('/')
    await expect(later.locator('html')).toHaveClass(/\bdark\b/)
    await later.close()
  })

  test.describe('sistema operativo in tema scuro', () => {
    test.use({ colorScheme: 'dark' })

    test('al primo accesso segue il sistema, e la scelta esplicita lo batte', async ({ page }) => {
      await page.goto('/')
      await expect(page.locator('html')).toHaveClass(/\bdark\b/)

      await page.locator('[data-testid="theme-toggle"]').click()
      await page.reload()
      // Chi ha scelto il chiaro ha detto qualcosa di più preciso di quanto dica
      // il suo sistema operativo, e continua a valere dopo il ricaricamento.
      await expect(page.locator('html')).not.toHaveClass(/\bdark\b/)
    })
  })

  test('il tema è deciso prima del primo pixel, non dopo l\'idratazione', async ({ request }) => {
    // Con `ssr: false` il browser dipinge lo sfondo del guscio prima che Vue
    // esista: senza uno script in testa al documento, chi ha il tema scuro
    // vedrebbe un lampo bianco a ogni caricamento. Si verifica sull'HTML
    // servito, perché a idratazione avvenuta il difetto è già passato e
    // qualunque asserzione sul DOM lo troverebbe a posto.
    const html = await (await request.get('/')).text()

    const boot = html.search(/prefers-color-scheme/)
    expect(boot, 'nessuno script che applica il tema nel documento servito').toBeGreaterThan(-1)

    // E deve venire prima del bundle: dopo, non avrebbe più niente da anticipare.
    const bundle = html.search(/<script[^>]*\bsrc="[^"]*\.js"/)
    expect(bundle).toBeGreaterThan(-1)
    expect(boot).toBeLessThan(bundle)
  })
})

// Vincolo di distribuzione (SPEC §2) e peso del JavaScript (R53-bis).

test.describe('build statica', () => {
  test('non lascia niente da servire a runtime', async () => {
    // Il gemello del controllo su `apps/web`, che là interroga `/api/_nuxt_island`
    // e si aspetta un 404. Qui quella prova non direbbe niente: il fallback
    // `/* /index.html 200` fa rispondere 200 a **qualunque** percorso, endpoint
    // Nitro compresi. L'unico posto in cui la differenza è visibile è l'output
    // della build: se `nuxt generate` avesse prodotto un server, accanto a
    // `public/` ci sarebbe il suo bundle — e su Cloudflare Pages non lo
    // eseguirebbe nessuno.
    const output = resolve(fileURLToPath(new URL('.', import.meta.url)), '../apps/dashboard/.output')
    const entries = readdirSync(output).filter(name => name !== 'nitro.json')

    expect(entries).toEqual(['public'])
  })

  /**
   * Tetto sul JavaScript che la dashboard scarica per aprirsi, non compresso.
   *
   * Misurato il 2026-08-17 su questa build: 186 KB in un file solo (71 KB in
   * gzip), quasi tutto runtime di Vue, Nuxt e vue-router. Il margine non serve a
   * far spazio: serve a lasciar crescere le sezioni che verranno.
   *
   * Il numero che questo tetto difende è un altro, e non compare qui perché non
   * è mai stato scaricato: `flowbite/dist/flowbite.min.js` pesa 131 KB non
   * compressi (29 KB in gzip). È il JavaScript dei componenti interattivi del
   * template — tendine, modali, tooltip, calendario — e non è nel bundle perché
   * quei comportamenti li scrive Vue, che c'è già. Importarlo sfonderebbe questo
   * tetto da solo, ed è esattamente il punto: la issue #513 ha appena tolto 66 KB
   * dal percorso critico del sito pubblico, e non li rimettiamo qui.
   */
  const JS_BUDGET = 260 * 1024

  test('il JavaScript d\'avvio resta sotto il tetto, senza il runtime di Flowbite', async ({ request }) => {
    const html = await (await request.get('/')).text()

    const sources = new Set([
      ...[...html.matchAll(/<script[^>]*\bsrc="([^"]+\.js)"/g)].map(match => match[1]!),
      ...[...html.matchAll(/<link\b[^>]*\brel="modulepreload"[^>]*\bhref="([^"]+\.js)"/g)].map(match => match[1]!),
    ])
    expect(sources.size, 'il guscio non dichiara nessuno script').toBeGreaterThan(0)

    const bodies = await Promise.all([...sources].map(async src => (await request.get(src)).text()))
    const bytes = bodies.reduce((sum, body) => sum + Buffer.byteLength(body), 0)

    expect(bytes, `JavaScript d'avvio della dashboard: ${Math.round(bytes / 1024)} KB su ${sources.size} file`)
      .toBeLessThanOrEqual(JS_BUDGET)
  })
})
