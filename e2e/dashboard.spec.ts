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
    // statico dei test. Senza, un refresh su /it/jobs/42 restituirebbe 404: è il
    // modo classico in cui una SPA statica si rompe solo in produzione, dove
    // nessuno ricarica mai la pagina durante lo sviluppo.
    //
    // Il prefisso di lingua non cambia questo meccanismo, e non deve: al server
    // statico i cinque prefissi non dicono niente — non esistono cinque
    // directory — e `/it/jobs/42` è un percorso come un altro, servito dallo
    // stesso guscio. A conoscerli è solo il router lato client.
    const response = await page.goto('/it/jobs/42')
    expect(response?.status()).toBe(200)

    // Il router Vue ha preso il controllo: la pagina non è più il guscio vuoto.
    await expect(page.locator('#__nuxt')).toHaveText(/\S/)
    await expect(page).toHaveURL('/it/jobs/42')
  })

  test('il guscio arriva su tutti e cinque i prefissi, e anche su ciò che non lo è', async ({ page, request }) => {
    // Il server non distingue: qualunque percorso è 200 e qualunque percorso è
    // il guscio. È l'unico modo in cui una SPA statica può funzionare, ed è il
    // motivo per cui l'elenco chiuso delle cinque lingue deve stare nel router
    // (`middleware/01.locale.global.ts`) e non nella distribuzione.
    for (const path of ['/en', '/it', '/es', '/de', '/fr', '/pt/jobs']) {
      expect((await request.get(path)).status(), path).toBe(200)
    }

    // E la differenza fra le cinque e il resto si vede solo dopo che il bundle
    // ha girato: `pt` non è una lingua, quindi è un segmento di percorso — che
    // viene prefissato come qualunque altro indirizzo senza lingua.
    await page.goto('/pt/jobs')
    await expect(page).toHaveURL('/en/pt/jobs')
  })
})

// Multilingua (SPEC §8-bis, R31–R33).
//
// Qui le asserzioni sono sui testi, contro la regola generale di questo file,
// perché il testo *è* il comportamento: «l'interfaccia è in tedesco» non si può
// verificare guardando la struttura della pagina. Sono cinque titoli, cambiano
// solo se cambia la pagina iniziale, e finché non cambia sono la sola prova che
// la traduzione arriva davvero fino allo schermo.
//
// A quelle si aggiungono le asserzioni sull'**indirizzo**, che è la cosa nuova:
// le rotte sono prefissate per lingua come sul sito pubblico, la radice smista
// e non ha contenuto proprio, e ogni indirizzo dichiara in che lingua aprirsi.
// Le due famiglie di asserzioni servono a cose diverse e vanno tenute insieme:
// l'indirizzo prova che il link è condivisibile, il testo prova che la lingua
// arriva davvero fino allo schermo.

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

      // La radice smista e non mostra contenuto proprio: si finisce su un
      // indirizzo che dichiara la lingua, e da lì in poi è quello a comandare.
      await expect(page).toHaveURL('/de')
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

      await expect(page).toHaveURL('/en')
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

      // Il prefisso è il codice della lingua, non il tag del browser: `/it`, e
      // non `/it-ch`, che sarebbe una sesta variante da mantenere.
      await expect(page).toHaveURL('/it')
      await expect(page.locator('h1')).toHaveText(TITLES.it)
    })
  })

  test.describe('l\'indirizzo comanda, non il browser', () => {
    test.use({ locale: 'de-DE' })

    test('un indirizzo con la lingua si apre in quella lingua (R31 non si applica)', async ({ page }) => {
      // È la precedenza scelta, e la ragione per cui il prefisso esiste: un
      // indirizzo che nomina la lingua deve aprirsi in quella, altrimenti non è
      // condivisibile — chi riceve il link vedrebbe la propria, e il prefisso
      // sarebbe decorativo. Vale anche quando il browser dice altro, che è il
      // caso di chiunque riceva un link da un collega.
      await page.goto('/fr')

      await expect(page.locator('h1')).toHaveText(TITLES.fr)
      await expect(page.locator('html')).toHaveAttribute('lang', 'fr')
      await expect(page.locator(SWITCHER)).toHaveValue('fr')
      // E non viene ricondotto al tedesco: l'indirizzo resta quello che si è
      // aperto, altrimenti sarebbe un link che non porta dove dice.
      await expect(page).toHaveURL('/fr')
    })

    test('guardare una pagina in un\'altra lingua non cambia la propria preferenza', async ({ page }) => {
      // Il rovescio della precedenza, e la parte che va tenuta ferma: aprire il
      // link di un collega non deve riscrivere in silenzio la lingua con cui si
      // apre tutto il resto. Solo il selettore scrive una preferenza.
      await page.goto('/fr')
      await expect(page.locator('h1')).toHaveText(TITLES.fr)

      await page.goto('/')
      await expect(page).toHaveURL('/de')
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

        // La lingua sta nell'indirizzo: il selettore la cambia lì, altrimenti
        // la pagina si leggerebbe in una lingua e si troverebbe su un indirizzo
        // che ne dichiara un'altra — cioè un indirizzo che mente a chi lo copia.
        await expect(page).toHaveURL(`/${code}`)
        await expect(page.locator('h1')).toHaveText(title)
        await expect(page.locator('html')).toHaveAttribute('lang', code)
        // Il titolo del documento segue la lingua: se `useHead` ricevesse un
        // oggetto statico resterebbe fermo a quella dell'avvio.
        await expect(page).toHaveTitle(`${title} · Postqron`)
      }

      await page.waitForLoadState('networkidle')
      expect(errors).toEqual([])
    })

    test('cambia indirizzo senza ricaricare, e senza perdere la pagina', async ({ page }) => {
      // Questo test provava che il selettore cambiava lingua **senza navigare**,
      // perché la lingua era stato dell'applicazione. Ora sta nell'indirizzo e
      // il selettore naviga: quella asserzione non descrive più il disegno.
      //
      // Ciò che invece descriveva — che il cambio è immediato e non porta via
      // quello che si stava guardando — resta desiderabile, e navigando è
      // *meno* scontato di prima: la chiave predefinita di Nuxt avrebbe
      // smontato e rimontato la pagina a ogni cambio. È quello che si verifica
      // qui, ed è il motivo per cui `app.vue` passa a `<NuxtPage>` una chiave
      // che è il percorso senza lingua.
      await page.goto('/')
      await expect(page.locator('[data-testid="health"]')).toBeVisible()

      // Un segno nel contesto JavaScript della pagina: sopravvive a una
      // navigazione lato client, non a un ricaricamento del documento.
      await page.evaluate(() => {
        (window as unknown as Record<string, unknown>).__stillHere = true
      })
      // E uno nel DOM della pagina, che sopravvive solo se il componente non
      // viene rimontato. È lo stato che una schermata vera avrebbe in memoria:
      // i dati già scaricati, la posizione in un elenco, ciò che si sta
      // scrivendo in un modulo.
      await page.evaluate(() => {
        document.querySelector('[data-testid="health"]')?.setAttribute('data-segno', 'x')
      })

      await page.selectOption(SWITCHER, 'fr')

      await expect(page).toHaveURL('/fr')
      await expect(page.locator('html')).toHaveAttribute('lang', 'fr')

      // Nessun ricaricamento: è una navigazione del router, non del browser.
      expect(
        await page.evaluate(() => (window as unknown as Record<string, unknown>).__stillHere),
      ).toBe(true)
      // E nessun rimontaggio: la stessa scheda, tradotta, non una rifatta.
      await expect(page.locator('[data-testid="health"]')).toHaveAttribute('data-segno', 'x')
    })

    test('cambiare lingua conserva la rotta profonda, la query e l\'ancora', async ({ page }) => {
      // Il caso che rende il selettore utile invece che pericoloso: chi è dentro
      // un elenco filtrato e cambia lingua deve restare in quell'elenco con quei
      // filtri. Tradurre la pagina buttandone via il contenuto è peggio che non
      // tradurla.
      await page.goto('/de/jobs/42?stato=fallito#storico')

      await page.selectOption(SWITCHER, 'es')

      await expect(page).toHaveURL('/es/jobs/42?stato=fallito#storico')
    })

    test('«indietro» torna alla pagina precedente, non alla lingua precedente', async ({ page }) => {
      // `replace` e non `push`: cambiare lingua non è andare da un'altra parte.
      // Con `push`, chi ne prova tre per trovare la propria dovrebbe premere
      // «indietro» tre volte per uscire dalla schermata.
      await page.goto('/de')
      await page.goto('/de/jobs/42')

      await page.selectOption(SWITCHER, 'it')
      await expect(page).toHaveURL('/it/jobs/42')

      await page.goBack()
      await expect(page).toHaveURL('/de')
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

      // Dove la scelta memorizzata conta davvero è **lo smistamento**: è la
      // radice a non dichiarare una lingua, ed è lì che si decide. Un
      // ricaricamento su `/it` tornerebbe in italiano comunque, perché lo dice
      // l'indirizzo, e non proverebbe niente.
      const later = await context.newPage()
      await mockBackend(later, true)
      await later.goto('/')
      await expect(later).toHaveURL('/it')
      await expect(later.locator('h1')).toHaveText(TITLES.it)
      await expect(later.locator(SWITCHER)).toHaveValue('it')
      await later.close()
    })

    test('un valore memorizzato non più valido non blocca l\'avvio', async ({ page }) => {
      // La chiave di `localStorage` sopravvive agli aggiornamenti: se un giorno
      // togliessimo una lingua, chi l'aveva scelta deve comunque poter entrare.
      await page.goto('/')
      await page.evaluate(() => window.localStorage.setItem('postqron:locale', 'pt'))

      // Si riparte dalla radice e non con un `reload()`: dopo lo smistamento
      // l'indirizzo dichiara già una lingua, e ricaricarlo non interrogherebbe
      // più il valore memorizzato.
      await page.goto('/')

      await expect(page).toHaveURL('/de')
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

  test('non dichiara lingue alternative: prefissata non vuol dire indicizzabile', async ({ page }) => {
    // La distinzione che questa issue deve tenere ferma. Le rotte hanno il
    // prefisso perché un indirizzo dev'essere condivisibile e componibile da
    // fuori, non perché qualcuno debba trovarlo: qui non esistono `hreflang` né
    // `canonical`, e copiarli dal sito pubblico ora che i cinque prefissi ci
    // sono è la modifica «coerente» da non fare.
    for (const path of ['/en', '/it/jobs/42']) {
      await page.goto(path)

      await expect(page.locator('meta[name="robots"]')).toHaveAttribute('content', /noindex/)
      await expect(page.locator('link[rel="alternate"]')).toHaveCount(0)
      await expect(page.locator('link[rel="canonical"]')).toHaveCount(0)
    }
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

    // Sulla radice della lingua è attiva la panoramica, e lo dice a chi ascolta
    // la pagina. I link portano il prefisso: `href="/"` manderebbe a smistare
    // di nuovo a ogni click, e dalla radice si tornerebbe alla lingua
    // *preferita* invece che a quella della pagina che si sta guardando.
    await expect(page.locator(`${SIDEBAR} nav a[href="/en"]`)).toHaveAttribute('aria-current', 'page')
  })

  test('su un indirizzo fuori dalle sezioni nessuna voce risulta attiva', async ({ page }) => {
    // La radice è un prefisso di qualunque percorso: senza il caso speciale in
    // `isActivePath()` la panoramica sarebbe attiva ovunque, e l'evidenziazione
    // smetterebbe di dire dove si è.
    // E il confronto non deve nemmeno inciampare nel prefisso: `/en` è la
    // panoramica, `/en/nessuna-sezione/42` no.
    await page.goto('/en/nessuna-sezione/42')

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
    await page.goto('/en/nessuna-sezione/42')

    await page.locator(NAV_TOGGLE).click()
    await expect(page.locator(SIDEBAR)).toBeVisible()

    await page.locator(`${SIDEBAR} nav a[href="/en"]`).click()

    await expect(page.locator(SIDEBAR)).toBeHidden()
    await expect(page.locator(`${SIDEBAR} nav a[href="/en"]`)).toHaveAttribute('aria-current', 'page')
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
    await page.goto('/en/questa-non-esiste')

    await expect(page.locator('h1')).toHaveText('Page not found')
    // Dentro il guscio: chi ci finisce deve poter andare altrove.
    await expect(page.locator(SIDEBAR)).toBeVisible()

    await page.getByRole('link', { name: 'Back to the overview' }).click()
    await expect(page).toHaveURL('/en')
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
