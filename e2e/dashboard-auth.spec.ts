import type { Page } from '@playwright/test'
import { expect, test } from '@playwright/test'
import { apiError, HEALTHY, mockBackend, SESSION } from './support/dashboard-api'

// Autenticazione della dashboard (R14): guardia di rotta, sessione lato client,
// accesso e registrazione.
//
// Sono e2e e non test unitari perché quasi niente di ciò che conta qui si vede
// senza un browser vero. «Non lampeggia la pagina di accesso prima della
// dashboard» è un'affermazione sull'ordine in cui le cose compaiono sullo
// schermo; «una rotta profonda ricade su index.html e dopo l'accesso ci si
// torna» attraversa il fallback del server statico, il router, la guardia e la
// barra degli indirizzi. Un test di montaggio non vedrebbe nessuna delle due.

/** Fa rispondere `POST /auth/login`, e ricorda cosa gli è arrivato. */
async function mockLogin(page: Page, outcome: { status: number, code?: string } = { status: 200 }) {
  const attempts: { email: string, password: string }[] = []

  await page.route('**/auth/login', async (route) => {
    attempts.push(route.request().postDataJSON())

    if (outcome.status === 200) {
      return route.fulfill({ json: SESSION })
    }
    return route.fulfill({
      status: outcome.status,
      contentType: 'application/json',
      body: apiError(outcome.code ?? 'invalid_credentials'),
    })
  })

  return attempts
}

/**
 * Aspetta che il modulo di accesso sia davvero in pagina.
 *
 * Serve prima di toccare `SessionControl`, e il motivo è una conferma del
 * disegno: `page.goto()` si risolve quando il documento è caricato, ma
 * l'applicazione non si è ancora montata — la guardia sta aspettando
 * `/auth/session`. Cambiare la sessione in quella finestra farebbe rispondere al
 * primo interrogatorio una risposta destinata al secondo.
 */
async function awaitSignInForm(page: Page) {
  await expect(page.locator('[data-testid="login-form"]')).toBeVisible()
}

/** Compila e invia il modulo di accesso. */
async function signIn(page: Page, email = 'mario.rossi@example.com', password = 'password-lunghissima') {
  await page.getByLabel('Email', { exact: true }).fill(email)
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.locator('[data-testid="auth-submit"]').click()
}

const SIDEBAR = '#sidebar'

test.describe('guardia di rotta', () => {
  test('senza sessione, una schermata protetta porta all\'accesso', async ({ page }) => {
    await mockBackend(page, false)

    await page.goto('/')

    // `/login` nudo, senza `?next=/`: la panoramica è già il ripiego, e
    // dichiararla metterebbe un parametro che non cambia niente nell'indirizzo
    // di chiunque apra la dashboard da scollegato.
    await expect(page).toHaveURL('/login')
    await expect(page.locator('h1')).toHaveText('Sign in')
    // Il guscio della dashboard non compare: l'accesso ha un layout suo, e la
    // barra laterale sarebbe un elenco di posti dove non si può andare.
    await expect(page.locator(SIDEBAR)).toHaveCount(0)
  })

  test('finché la sessione è ignota non si mostra niente, in nessuna delle due direzioni', async ({ page }) => {
    // È **il** comportamento di questa issue, ed è l'unico verificabile solo
    // fermando il tempo: al primo caricamento la SPA non sa se la sessione è
    // valida, e il momento in cui non sa è gestito non montando l'applicazione.
    // Senza, l'utente vedrebbe lampeggiare l'accesso prima della dashboard — o,
    // peggio, la dashboard prima di essere buttato all'accesso.
    let release = () => {}
    const held = new Promise<void>((resolve) => { release = resolve })

    await page.route('**/healthz', route => route.fulfill({ json: HEALTHY }))
    await page.route('**/auth/session', async (route) => {
      await held
      await route.fulfill({ json: SESSION })
    })

    await page.goto('/')

    // Applicazione non montata: il guscio servito è ancora vuoto. Non c'è né la
    // dashboard né il modulo di accesso, perché non si sa ancora quale delle due
    // sia la risposta giusta.
    await expect(page.locator('#__nuxt')).toBeEmpty()
    await expect(page.locator('h1')).toHaveCount(0)

    release()

    await expect(page.locator('h1')).toHaveText('Overview')
    await expect(page.locator(SIDEBAR)).toBeVisible()
  })

  test('chi è già collegato non rivede il modulo di accesso', async ({ page }) => {
    await mockBackend(page, true)

    await page.goto('/login')

    await expect(page).toHaveURL('/')
    await expect(page.locator('h1')).toHaveText('Overview')
  })

  test('se il backend non risponde non si viene cacciati: si resta, e la vista lo dichiara', async ({ page }) => {
    // `unavailable` non è «non collegato». Trattarlo come tale manderebbe
    // all'accesso chi ha una sessione buona e un attimo di rete storta — dove
    // peraltro nemmeno l'accesso funzionerebbe. Si passa, e il guasto lo dice la
    // vista con il proprio stato d'errore (R56).
    await page.route('**/auth/session', route => route.fulfill({ status: 500, body: '' }))
    await page.route('**/healthz', route => route.fulfill({ status: 500, body: '' }))

    await page.goto('/')

    await expect(page).toHaveURL('/')
    await expect(page.locator('[data-testid="state-error"]')).toBeVisible()
    await expect(page.locator('[data-testid="state-retry"]')).toBeVisible()
  })
})

test.describe('ritorno dove si voleva andare', () => {
  test('una rotta profonda sopravvive all\'accesso', async ({ page }) => {
    // Il caso del segnalibro e del link in un\'email: `/jobs/42` ricade su
    // index.html (regola `/* /index.html 200`), la guardia manda all'accesso, e
    // dopo l'accesso si deve tornare **lì**, non alla panoramica.
    const control = await mockBackend(page, false)
    await mockLogin(page)

    await page.goto('/jobs/42')

    await expect(page).toHaveURL('/login?next=/jobs/42')
    await expect(page.locator('[data-testid="session-returning"]')).toBeVisible()

    await awaitSignInForm(page)
    control.restore()
    await signIn(page)

    await expect(page).toHaveURL('/jobs/42')
  })

  test('dopo l\'accesso, «indietro» non riporta al modulo', async ({ page }) => {
    // Il redirect della guardia e quello dell'accesso sono entrambi `replace`:
    // senza, il tasto «indietro» tornerebbe alla schermata di accesso, che la
    // guardia rimanderebbe subito avanti — un rimbalzo senza uscita apparente.
    const control = await mockBackend(page, false)
    await mockLogin(page)

    await page.goto('/jobs/42')
    await awaitSignInForm(page)
    control.restore()
    await signIn(page)
    await expect(page).toHaveURL('/jobs/42')

    await page.goBack()
    await expect(page).not.toHaveURL(/\/login/)
  })

  test('un indirizzo di ritorno verso un altro sito viene ignorato', async ({ page }) => {
    // Redirect aperto: `?next=` viaggia nella barra degli indirizzi, e un
    // rimando fuori dal nostro dominio subito dopo che l'utente ha scritto la
    // password è la forma classica del problema. Non è un errore da mostrare:
    // si ricade sulla panoramica e basta.
    const control = await mockBackend(page, false)
    await mockLogin(page)

    await page.goto('/login?next=https://phishing.example/')
    await awaitSignInForm(page)
    control.restore()
    await signIn(page)

    await expect(page).toHaveURL('/')
  })
})

test.describe('sessione che finisce a metà lavoro', () => {
  test('un 401 riporta all\'accesso, dice perché, e poi restituisce la schermata', async ({ page }) => {
    // Scadenza, o revoca da un altro dispositivo. La guardia non c'entra: qui la
    // sessione muore mentre l'utente sta guardando una schermata, e se ne
    // accorge una richiesta qualunque.
    const control = await mockBackend(page, true)
    await mockLogin(page)

    let live = true
    await page.route('**/healthz', (route) => {
      if (!live) {
        return route.fulfill({ status: 401, contentType: 'application/json', body: apiError('unauthenticated') })
      }
      return route.fulfill({ json: HEALTHY })
    })

    // La query fa parte di dove si era: i filtri di un elenco stanno lì, e
    // tornare all'elenco senza i suoi filtri è comunque aver perso il posto. È
    // il motivo per cui si ricorda `fullPath` e non `path`.
    await page.goto('/?scheda=salute')
    await expect(page.locator('[data-testid="health"]')).toBeVisible()

    // La sessione muore mentre l'utente sta guardando la schermata. Nessuna
    // navigazione: se ne accorge la prima richiesta che parte da lì.
    live = false
    control.revoke()
    await page.locator('[data-testid="health-refresh"]').click()

    await expect(page).toHaveURL(/\/login\?next=/)
    // **Detto**, non subito: chi si vede comparire il modulo di accesso in mezzo
    // a un lavoro deve sapere che è finita la sessione, non concludere che
    // l'applicazione si è rotta e gli ha fatto perdere quello che stava facendo.
    await expect(page.locator('[data-testid="session-interrupted"]')).toBeVisible()

    live = true
    control.restore()
    await signIn(page)

    await expect(page).toHaveURL('/?scheda=salute')
    await expect(page.locator('h1')).toHaveText('Overview')
    // L'avviso è consumato: non deve ricomparire al prossimo accesso volontario.
    await page.locator('[data-testid="account-toggle"]').click()
    await page.locator('[data-testid="sign-out"]').click()
    await expect(page).toHaveURL('/login')
    await expect(page.locator('[data-testid="session-interrupted"]')).toHaveCount(0)
  })
})

test.describe('uscita', () => {
  test('il menu dell\'account chiude la sessione, e la schermata protetta non torna', async ({ page }) => {
    await mockBackend(page, true)

    await page.goto('/')
    await page.locator('[data-testid="account-toggle"]').click()

    const menu = page.locator('[data-testid="account-menu"]')
    await expect(menu).toBeVisible()
    await expect(menu).toContainText('mario.rossi@example.com')

    await page.locator('[data-testid="sign-out"]').click()

    await expect(page).toHaveURL('/login')
    await expect(page.locator('h1')).toHaveText('Sign in')
    // E la sessione è chiusa davvero: riaprire la schermata protetta rimanda qui.
    await page.goto('/')
    await expect(page).toHaveURL(/\/login/)
  })

  test('il menu si chiude con Esc e restituisce il fuoco al pulsante', async ({ page }) => {
    await mockBackend(page, true)

    await page.goto('/')
    const toggle = page.locator('[data-testid="account-toggle"]')
    await toggle.click()
    await expect(page.locator('[data-testid="account-menu"]')).toBeVisible()

    await page.keyboard.press('Escape')

    await expect(page.locator('[data-testid="account-menu"]')).toHaveCount(0)
    // Senza il ritorno del fuoco, il tabulatore successivo ripartirebbe
    // dall'inizio della pagina (R54, WCAG 2.2 2.1.2).
    await expect(toggle).toBeFocused()
  })
})

test.describe('cosa si dice a chi sbaglia', () => {
  test('«utente inesistente» e «password sbagliata» danno lo stesso identico messaggio', async ({ page }) => {
    // La proprietà da non rompere. Il backend si difende dall'enumerazione degli
    // account fino a pareggiare i **tempi** di risposta dei due casi — verifica
    // una password finta quando l'utente non c'è. Due messaggi diversi qui
    // annullerebbero tutto quel lavoro con una frase: chi vuole sapere se un
    // indirizzo è registrato smetterebbe di cronometrare e leggerebbe.
    await mockBackend(page, false)
    await mockLogin(page, { status: 401, code: 'invalid_credentials' })

    await page.goto('/login')

    await signIn(page, 'non-esiste@example.com', 'password-qualunque')
    const primo = await page.locator('[data-testid="auth-error"]').textContent()

    await signIn(page, 'mario.rossi@example.com', 'password-sbagliata')
    const secondo = await page.locator('[data-testid="auth-error"]').textContent()

    expect(primo?.trim()).toBe('Email or password is not correct.')
    expect(secondo).toBe(primo)
  })

  test('troppi tentativi non si legge come «controlla i dati inseriti»', async ({ page }) => {
    // Un 429 ricade fra i 4xx generici: senza un ramo suo manderebbe a
    // ricontrollare una password che magari era giusta.
    await mockBackend(page, false)
    await mockLogin(page, { status: 429, code: 'rate_limited' })

    await page.goto('/login')
    await signIn(page)

    await expect(page.locator('[data-testid="auth-error"]'))
      .toHaveText('Too many attempts. Wait a few minutes, then try again.')
  })

  test('un rifiuto non fa riscrivere l\'indirizzo, ma azzera la password', async ({ page }) => {
    await mockBackend(page, false)
    await mockLogin(page, { status: 401 })

    await page.goto('/login')
    await signIn(page, 'mario.rossi@example.com', 'password-sbagliata')
    await expect(page.locator('[data-testid="auth-error"]')).toBeVisible()

    await expect(page.getByLabel('Email', { exact: true })).toHaveValue('mario.rossi@example.com')
    await expect(page.getByLabel('Password', { exact: true })).toHaveValue('')
  })
})

test.describe('registrazione', () => {
  test('l\'esito è lo stesso che l\'indirizzo sia libero o già registrato', async ({ page }) => {
    // `POST /auth/register` risponde 202 identico nei due casi, apposta. L'unico
    // modo per l'interfaccia di rovinarlo è dire più di quello che sa: niente
    // «account creato», che sull'indirizzo altrui sarebbe una bugia e su quello
    // proprio una conferma di esistenza per chiunque la provi.
    await mockBackend(page, false)
    await page.route('**/auth/register', route =>
      route.fulfill({ status: 202, json: { status: 'accepted', message: '…' } }))

    await page.goto('/register')
    await page.getByLabel('Full name', { exact: true }).fill('Mario Rossi')
    await page.getByLabel('Email', { exact: true }).fill('libero@example.com')
    await page.getByLabel('Password', { exact: true }).fill('password-lunghissima')
    await page.locator('[data-testid="auth-submit"]').click()

    const accepted = page.locator('[data-testid="register-accepted"]')
    await expect(accepted).toHaveText('If the address can be used, we have sent an email with the next steps.')
    // Nessun accesso automatico: entrare vorrebbe dire che l'account è nostro.
    await expect(page.locator('[data-testid="account-toggle"]')).toHaveCount(0)
  })

  test('una password troppo corta lo dice prima di partire', async ({ page }) => {
    // La verifica è quella del browser (`minlength`), tradotta da lui: non
    // sostituisce la regola del backend, evita solo un viaggio inutile.
    let requested = false
    await mockBackend(page, false)
    await page.route('**/auth/register', (route) => {
      requested = true
      return route.fulfill({ status: 202, json: {} })
    })

    await page.goto('/register')
    await page.getByLabel('Full name', { exact: true }).fill('Mario Rossi')
    await page.getByLabel('Email', { exact: true }).fill('mario@example.com')
    await page.getByLabel('Password', { exact: true }).fill('corta')
    await page.locator('[data-testid="auth-submit"]').click()

    await expect(page.locator('[data-testid="register-accepted"]')).toHaveCount(0)
    expect(requested).toBe(false)
  })

  test('il requisito della password è scritto prima di sceglierla, e legato al campo', async ({ page }) => {
    await mockBackend(page, false)
    await page.goto('/register')

    const password = page.getByLabel('Password', { exact: true })
    const hintId = await password.getAttribute('aria-describedby')
    expect(hintId).toBeTruthy()
    // Legato con `aria-describedby`: senza, quella riga è testo lì vicino, e chi
    // non vede la pagina non la sente mai.
    await expect(page.locator(`#${hintId}`)).toHaveText('At least 12 characters.')
  })
})

// Multilingua sulle schermate nuove (R31–R32).
//
// Il selettore resta nel guscio dell'accesso e non solo in quello della
// dashboard: è la prima schermata che si vede, e chi non legge la lingua
// indovinata dal browser dovrebbe altrimenti entrare al buio proprio nel momento
// in cui deve scrivere una password.

const SWITCHER = '[data-testid="locale-switcher"]'

const SIGN_IN_TITLES = {
  en: 'Sign in',
  it: 'Accedi',
  es: 'Iniciar sesión',
  de: 'Anmelden',
  fr: 'Se connecter',
} as const

const SIGN_UP_TITLES = {
  en: 'Create an account',
  it: 'Crea un account',
  es: 'Crear una cuenta',
  de: 'Konto erstellen',
  fr: 'Créer un compte',
} as const

test.describe('lingua delle schermate di autenticazione', () => {
  test('accesso e registrazione esistono in tutte e cinque le lingue (R32)', async ({ page }) => {
    await mockBackend(page, false)

    await page.goto('/login')
    for (const [code, title] of Object.entries(SIGN_IN_TITLES)) {
      await page.selectOption(SWITCHER, code)
      await expect(page.locator('h1')).toHaveText(title)
      await expect(page.locator('html')).toHaveAttribute('lang', code)
      await expect(page).toHaveTitle(`${title} · Postqron`)
    }

    await page.goto('/register')
    for (const [code, title] of Object.entries(SIGN_UP_TITLES)) {
      await page.selectOption(SWITCHER, code)
      await expect(page.locator('h1')).toHaveText(title)
    }
  })

  test.describe('browser in tedesco', () => {
    test.use({ locale: 'de-DE' })

    test('anche il rimbalzo verso l\'accesso arriva nella lingua giusta (R31)', async ({ page }) => {
      // Il rilevamento vale prima dell'autenticazione: la lingua del profilo
      // (R33) non c'è ancora, e chi non è collegato non ne ha comunque una.
      await mockBackend(page, false)

      await page.goto('/jobs/42')

      await expect(page.locator('h1')).toHaveText(SIGN_IN_TITLES.de)
    })
  })
})
