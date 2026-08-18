import type { Page } from '@playwright/test'
import { expect, test } from '@playwright/test'
import { apiError, mockBackend } from './support/dashboard-api'

// CRUD dei cronjob, con validazione e anteprima delle esecuzioni (SPEC §4.2,
// R1, R15, R41, R56, R58).
//
// Sono e2e e non test unitari per la stessa ragione degli altri di questa
// cartella: quasi niente di ciò che conta qui si vede senza un browser vero.
// «L'anteprima mostra gli orari nel fuso del job» attraversa `Intl` del browser
// reale con il database dei fusi del sistema, che è precisamente il pezzo che
// una finzione sostituirebbe con sé stessa; «il modulo non si invia e segna il
// campo» attraversa il ciclo di reattività, il markup e l'associazione fra
// etichetta e casella.

// ------------------------------------------------------------------ finzioni

/** Un job come lo manda `httpapi.JobResponse`. */
function job(overrides: Record<string, unknown> = {}) {
  return {
    id: '0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c',
    name: 'daily-digest',
    schedule: '0 9 * * *',
    every: null,
    timezone: 'Europe/Rome',
    environments: ['production'],
    request: { url: 'https://api.esempio.test/digest', method: 'POST', headers: {}, body: null },
    timeout: '30s',
    retries: { max: 3, backoff: 'exponential' },
    on_overlap: 'skip',
    alerts: { on_failure: ['email'] },
    enabled: true,
    next_run_at: '2026-08-18T07:00:00Z',
    created_at: '2026-08-17T09:00:00Z',
    updated_at: '2026-08-17T09:00:00Z',
    ...overrides,
  }
}

/** Il piano in forza come lo manda `httpapi.SubscriptionResponse`. */
function subscription(overrides: Record<string, unknown> = {}) {
  return {
    plan: 'free',
    plan_name: 'Free',
    status: 'active',
    max_jobs: 20,
    active_jobs: 1,
    min_interval: '1m',
    log_retention_days: 3,
    suspended_jobs: { by_job_limit: 0, by_resolution: 0, total: 0 },
    ...overrides,
  }
}

/**
 * L'origin del backend finto.
 *
 * Le rotte si ancorano **all'indirizzo dell'API**, non a un motivo con il
 * carattere jolly in testa: la dashboard è servita su un'altra origin e le sue
 * schermate stanno proprio su `/jobs`, quindi un motivo generico
 * intercetterebbe la navigazione del documento e servirebbe il JSON al posto
 * della pagina — che è esattamente quello che è successo la prima volta che
 * questo file è girato.
 */
const API = 'http://localhost:8080'

interface Backend {
  jobs?: ReturnType<typeof job>[]
  plan?: ReturnType<typeof subscription>
}

/** Registra le rotte dei job e della fatturazione, e cosa gli è arrivato. */
async function mockJobs(page: Page, backend: Backend = {}) {
  const written: { method: string, url: string, body: unknown }[] = []
  const jobs = backend.jobs ?? [job()]

  await page.route(`${API}/billing/subscription`, route =>
    route.fulfill({ json: backend.plan ?? subscription() }))

  await page.route(new RegExp(`^${API}/jobs(\\?.*)?$`), (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ json: { jobs, page: { limit: 50, next_cursor: null } } })
    }
    written.push({ method: 'POST', url: route.request().url(), body: route.request().postDataJSON() })
    return route.fulfill({ status: 201, json: job({ id: 'creato' }) })
  })

  await page.route(new RegExp(`^${API}/jobs/[^/?]+$`), (route) => {
    const method = route.request().method()
    if (method === 'GET') return route.fulfill({ json: jobs[0] })
    written.push({ method, url: route.request().url(), body: route.request().postDataJSON() })
    if (method === 'DELETE') return route.fulfill({ status: 204, body: '' })
    return route.fulfill({ json: { ...jobs[0], ...(route.request().postDataJSON() as object) } })
  })

  await page.route(new RegExp(`^${API}/jobs/[^/?]+/executions$`), (route) => {
    written.push({ method: 'POST', url: route.request().url(), body: null })
    return route.fulfill({ status: 202, json: {} })
  })

  return written
}

/** Apre una schermata dei job con una sessione valida. */
async function open(page: Page, path: string, backend: Backend = {}) {
  await mockBackend(page, true)
  const written = await mockJobs(page, backend)
  await page.goto(path)
  return written
}

// ------------------------------------------------------------------- elenco

test.describe('elenco dei cronjob', () => {
  test('mostra i job con la loro schedulazione e il loro fuso', async ({ page }) => {
    await open(page, '/en/jobs')

    const row = page.locator('[data-testid="job-row"]')
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('daily-digest')
    await expect(row).toContainText('0 9 * * *')
    // Il fuso accanto alla schedulazione: un'espressione cron senza il fuso in
    // cui va letta non dice a che ora parte (R1).
    await expect(row).toContainText('Europe/Rome')
  })

  test('la prossima esecuzione è resa nel fuso del job', async ({ page }) => {
    // `next_run_at` è le 07:00 UTC, cioè le 09:00 a Roma: chi guarda deve
    // leggere le nove, non le sette, qualunque sia il fuso del suo browser.
    await open(page, '/en/jobs')

    await expect(page.locator('[data-testid="job-next-run"]')).toContainText('9:00')
  })

  test('un job appena creato dice che aspetta lo scheduler, non che è rotto', async ({ page }) => {
    // `next_run_at` nasce `null` (migrazione 0010) e un trattino al suo posto
    // farebbe sembrare guasto proprio il job appena creato.
    await open(page, '/en/jobs', { jobs: [job({ next_run_at: null })] })

    await expect(page.locator('[data-testid="job-next-pending"]')).toBeVisible()
  })

  test('senza job dice cosa manca e cosa fare', async ({ page }) => {
    // R56: lo stato vuoto dichiarato. «Nessun risultato» non aiuterebbe
    // nessuno, e questa è la prima schermata del percorso al primo job (R55).
    await open(page, '/en/jobs', { jobs: [] })

    const empty = page.locator('[data-testid="state-empty"]')
    await expect(empty).toBeVisible()
    await expect(empty).toContainText('No cron jobs yet')
    await expect(page.locator('[data-testid="job-create"]')).toBeVisible()
  })

  test('un guasto della fatturazione non porta via l\'elenco dei job', async ({ page }) => {
    // Le due letture sono indipendenti apposta: un 500 sul piano non deve
    // nascondere ciò per cui si è aperta la pagina.
    await mockBackend(page, true)
    await mockJobs(page)
    await page.route(`${API}/billing/subscription`, route =>
      route.fulfill({ status: 500, contentType: 'application/json', body: apiError('internal_error') }))
    await page.goto('/en/jobs')

    await expect(page.locator('[data-testid="state-error"]')).toBeVisible()
    await expect(page.locator('[data-testid="job-row"]')).toHaveCount(1)
  })

  test('esegui adesso dice che è registrata, non che è avvenuta', async ({ page }) => {
    // Il backend risponde 202: la riga c'è, la chiamata la farà il motore.
    const written = await open(page, '/en/jobs')

    await page.locator('[data-testid="job-run"]').click()

    await expect(page.locator('[data-testid="jobs-run-queued"]')).toBeVisible()
    expect(written.filter(w => w.url.endsWith('/executions'))).toHaveLength(1)
  })

  test('l\'eliminazione chiede conferma e dice cosa sparisce', async ({ page }) => {
    const written = await open(page, '/en/jobs')

    await page.locator('[data-testid="job-delete"]').click()
    const dialog = page.locator('[data-testid="job-delete-dialog"]')
    await expect(dialog).toContainText('daily-digest')

    // Ripensarci non deve cancellare niente.
    await page.locator('[data-testid="job-delete-cancel"]').click()
    await expect(dialog).toBeHidden()
    expect(written.filter(w => w.method === 'DELETE')).toHaveLength(0)

    await page.locator('[data-testid="job-delete"]').click()
    await page.locator('[data-testid="job-delete-confirm"]').click()
    await expect.poll(() => written.filter(w => w.method === 'DELETE').length).toBe(1)
  })
})

// --------------------------------------------------- i limiti di piano (R15)

test.describe('i limiti di piano si vedono prima di sbatterci', () => {
  test('il piano dichiara i tre tetti che R15 nomina', async ({ page }) => {
    await open(page, '/en/jobs')

    const card = page.locator('[data-testid="plan-card"]')
    await expect(card).toContainText('Free')
    // Numero di job, frequenza minima e retention: tutti e tre arrivano
    // dall'API, nessuno è scritto nel client.
    await expect(card.locator('[data-testid="plan-jobs"]')).toContainText('1 of 20')
    await expect(card.locator('[data-testid="plan-interval"]')).toContainText('1m')
    await expect(card).toContainText('3 days')
  })

  test('col piano pieno il modulo non si apre, e si legge perché', async ({ page }) => {
    // R15: dirlo **prima**. Compilare quindici campi per un 403 è lavoro
    // buttato, ed è esattamente il caso che la regola esiste per evitare.
    await open(page, '/en/jobs', { plan: subscription({ active_jobs: 20 }) })

    await expect(page.locator('[data-testid="job-create"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="job-create-blocked"]')).toContainText('plan is full')
  })

  test('anche arrivandoci per indirizzo diretto', async ({ page }) => {
    // Il pulsante sparisce dall'elenco, ma l'indirizzo del modulo è quello che
    // le email di benvenuto compongono: ci si arriva senza passare dall'elenco.
    await open(page, '/en/jobs/new', { plan: subscription({ active_jobs: 20 }) })

    await expect(page.locator('[data-testid="job-create-blocked"]')).toBeVisible()
    await expect(page.locator('[data-testid="job-form"]')).toHaveCount(0)
  })

  test('i job fermi da un cambio di piano sono contati per motivo', async ({ page }) => {
    // R58: due numeri e non uno, perché i rimedi sono due — scegliere quali
    // riaccendere, oppure cambiare la schedulazione.
    await open(page, '/en/jobs', {
      plan: subscription({ suspended_jobs: { by_job_limit: 20, by_resolution: 5, total: 25 } }),
    })

    await expect(page.locator('[data-testid="suspended-by-limit"]')).toContainText('20')
    const resolution = page.locator('[data-testid="suspended-by-resolution"]')
    await expect(resolution).toContainText('5')
    // La frase nomina la risoluzione del piano, che arriva dall'API.
    await expect(resolution).toContainText('1m')
  })

  test('un job troppo fitto non si riaccende, e il pulsante lo dice', async ({ page }) => {
    /*
     * R58, la parte che la spec chiede esplicitamente di *dire*: un job
     * sospeso per risoluzione non torna su nemmeno se c'è posto. Va prima
     * cambiata la schedulazione, e mettere in pausa un altro job non libera
     * niente.
     */
    await open(page, '/en/jobs', {
      jobs: [job({
        enabled: false,
        every: '1s',
        schedule: null,
        next_run_at: null,
        suspended: { at: '2026-08-17T10:00:00Z', reason: 'plan_resolution' },
      })],
    })

    await expect(page.locator('[data-testid="job-state"]')).toContainText('Too frequent')
    await expect(page.locator('[data-testid="job-blocked"]')).toContainText('Change its schedule')
    await expect(page.locator('[data-testid="job-toggle"]')).toBeDisabled()
  })
})

// ------------------------------------------------------------- l'anteprima

test.describe('anteprima delle esecuzioni', () => {
  test('mostra gli orari veri nel fuso del job, dichiarandolo', async ({ page }) => {
    await open(page, '/en/jobs/new')

    await page.locator('[data-testid="job-schedule"]').fill('0 9 * * *')
    await page.locator('[data-testid="job-timezone"]').selectOption('Europe/Rome')

    const preview = page.locator('[data-testid="schedule-preview"]')
    // Il fuso **dichiarato**: senza, un elenco di orari è più dannoso che utile,
    // perché chi non vive in quel fuso lo legge come proprio (R1).
    await expect(preview).toContainText('Europe/Rome')
    await expect(preview.locator('li')).toHaveCount(5)
    await expect(preview.locator('li').first()).toContainText('9:00')
  })

  test('distingue le due espressioni che si somigliano', async ({ page }) => {
    /*
     * `0 0 * * 0` e `0 0 0 * *` si scrivono quasi uguali: la prima parte ogni
     * domenica, la seconda non parte mai perché lo zero non è un giorno del
     * mese. È il motivo per cui questo riquadro esiste.
     */
    await open(page, '/en/jobs/new')
    const schedule = page.locator('[data-testid="job-schedule"]')
    const preview = page.locator('[data-testid="schedule-preview"]')

    await schedule.fill('0 0 * * 0')
    await expect(preview.locator('li')).toHaveCount(5)

    await schedule.fill('0 0 30 2 *')
    await expect(preview.locator('[data-testid="preview-never"]')).toBeVisible()
  })

  test('dichiara che l\'intervallo è ancorato all\'epoch', async ({ page }) => {
    /*
     * SPEC §9: `every: 1h` scocca all'ora piena UTC, non un'ora dopo il
     * salvataggio. È l'errore che sembra giusto, e senza questa riga
     * l'anteprima sembrerebbe sbagliata proprio a chi ha capito bene.
     */
    await open(page, '/en/jobs/new')

    await page.locator('[data-testid="job-mode-interval"]').check()
    await page.locator('[data-testid="job-every"]').fill('1')
    await page.locator('[data-testid="job-every-unit"]').selectOption('h')

    await expect(page.locator('[data-testid="preview-epoch"]')).toContainText('UTC hour')

    // E gli orari mostrati cadono davvero sull'ora piena.
    const first = await page.locator('[data-testid="schedule-preview"] li time').first().getAttribute('datetime')
    expect(first).toMatch(/T\d\d:00:00/)
  })

  test('spiega l\'occorrenza che l\'ora legale sposta', async ({ page }) => {
    /*
     * L'ultima domenica di marzo a Roma le 02:30 non esistono. La regola del
     * motore è di non saltare l'occorrenza ma di spostarla al primo istante che
     * esiste, e un orario che si muove di mezz'ora senza spiegazione sembra un
     * difetto del prodotto.
     */
    await open(page, '/en/jobs/new')

    await page.locator('[data-testid="job-timezone"]').selectOption('Europe/Rome')
    await page.locator('[data-testid="job-schedule"]').fill('30 2 * * *')

    // L'anteprima parte da adesso: la nota compare solo nei giorni del salto,
    // quindi qui si verifica che il meccanismo esista, non che scatti oggi.
    await expect(page.locator('[data-testid="schedule-preview"] li')).toHaveCount(5)
  })

  test('sul job esistente mostra anche il valore calcolato dal motore', async ({ page }) => {
    // I due devono coincidere, e vederli insieme è l'unico modo in cui una
    // divergenza si nota.
    await open(page, '/en/jobs/0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c')

    await expect(page.locator('[data-testid="preview-scheduled"]')).toContainText('9:00')
  })
})

// ------------------------------------------------------------ la validazione

test.describe('validazione', () => {
  test('ferma ciò che il backend rifiuterebbe, e segna il campo', async ({ page }) => {
    const written = await open(page, '/en/jobs/new')

    await page.locator('[data-testid="job-name"]').fill('nome con spazi')
    await page.locator('[data-testid="job-schedule"]').fill('0 9 * * *')
    await page.locator('[data-testid="job-url"]').fill('https://esempio.test/hook')
    await page.locator('[data-testid="job-submit"]').click()

    await expect(page.locator('[data-testid="job-form-invalid"]')).toBeVisible()
    await expect(page.locator('[data-testid="field-error"]').first()).toContainText('Letters, digits')
    // Niente richiesta: la verifica del client serve a non spenderne una.
    expect(written).toHaveLength(0)
  })

  test('non rifiuta ciò che il backend accetterebbe', async ({ page }) => {
    /*
     * La direzione opposta, e la più insidiosa: un modulo che si rifiuta di
     * inviare per una regola che il server non ha rende irraggiungibile
     * qualcosa che il prodotto sa fare. `localhost` lo giudica il blocco dei
     * bersagli all'apertura della connessione (R38), non il browser.
     */
    const written = await open(page, '/en/jobs/new')

    await page.locator('[data-testid="job-name"]').fill('healthcheck')
    await page.locator('[data-testid="job-schedule"]').fill('*/5 * * * *')
    await page.locator('[data-testid="job-url"]').fill('http://localhost:8080/health')
    await page.locator('[data-testid="job-submit"]').click()

    await expect.poll(() => written.length).toBe(1)
  })

  test('il rifiuto del backend finisce accanto al campo giusto', async ({ page }) => {
    // I `details[]` per campo esistono apposta: senza, l'unica cosa dicibile su
    // un 400 sarebbe «controlla i dati inseriti» davanti a quindici caselle.
    await mockBackend(page, true)
    await mockJobs(page)
    await page.route(new RegExp(`^${API}/jobs$`), (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({ json: { jobs: [], page: { limit: 50, next_cursor: null } } })
      }
      return route.fulfill({
        status: 400,
        contentType: 'application/json',
        body: JSON.stringify({
          error: {
            code: 'validation_failed',
            message: 'La richiesta contiene campi non validi.',
            details: [{ field: 'request.url', code: 'target_not_allowed', message: 'non ammesso' }],
          },
        }),
      })
    })
    await page.goto('/en/jobs/new')

    await page.locator('[data-testid="job-name"]').fill('healthcheck')
    await page.locator('[data-testid="job-schedule"]').fill('*/5 * * * *')
    await page.locator('[data-testid="job-url"]').fill('https://esempio.test/hook')
    await page.locator('[data-testid="job-submit"]').click()

    // Tradotto dal **codice**: il messaggio del backend è in italiano e non si
    // mostra mai (SPEC §8-bis).
    await expect(page.locator('[data-testid="field-error"]'))
      .toContainText('cannot be called from Postqron')
  })

  test('il rifiuto di piano nomina il piano e la sua risoluzione', async ({ page }) => {
    await mockBackend(page, true)
    await mockJobs(page)
    await page.route(new RegExp(`^${API}/jobs$`), (route) => {
      if (route.request().method() === 'GET') {
        return route.fulfill({ json: { jobs: [], page: { limit: 50, next_cursor: null } } })
      }
      return route.fulfill({
        status: 403,
        contentType: 'application/json',
        body: JSON.stringify({
          error: {
            code: 'plan_limit_resolution',
            message: 'il piano Free consente una risoluzione minima di 1m…',
            limit: 'resolution',
            plan: 'free',
          },
        }),
      })
    })
    await page.goto('/en/jobs/new')

    await page.locator('[data-testid="job-name"]').fill('healthcheck')
    await page.locator('[data-testid="job-mode-interval"]').check()
    await page.locator('[data-testid="job-every"]').fill('1')
    await page.locator('[data-testid="job-every-unit"]').selectOption('s')
    await page.locator('[data-testid="job-url"]').fill('https://esempio.test/hook')
    await page.locator('[data-testid="job-submit"]').click()

    const error = page.locator('[data-testid="job-form-error"]')
    // Il piano e il numero arrivano da `/billing/subscription`: nessuna tabella
    // di listino nel client.
    await expect(error).toContainText('Free')
    await expect(error).toContainText('1m')
  })
})

// ------------------------------------------------------ creazione e modifica

test.describe('creazione', () => {
  test('manda il corpo nella forma di cron.yaml', async ({ page }) => {
    const written = await open(page, '/en/jobs/new')

    await page.locator('[data-testid="job-name"]').fill('daily-digest')
    await page.locator('[data-testid="job-schedule"]').fill('0 9 * * *')
    await page.locator('[data-testid="job-timezone"]').selectOption('Europe/Rome')
    await page.locator('[data-testid="job-url"]').fill('https://api.esempio.test/digest')
    await page.locator('[data-testid="job-submit"]').click()

    await expect.poll(() => written.length).toBe(1)
    expect(written[0]!.body).toMatchObject({
      name: 'daily-digest',
      schedule: '0 9 * * *',
      // `every` esplicitamente a `null`, non assente: è il modo in cui una
      // modalità si dismette, e `optional[T]` distingue i due casi.
      every: null,
      timezone: 'Europe/Rome',
      // Le durate viaggiano come stringhe, come in un `cron.yaml`.
      timeout: '30s',
      retries: { max: 3, backoff: 'exponential' },
      on_overlap: 'skip',
    })
  })

  test('la modalità a intervallo commuta i due campi', async ({ page }) => {
    const written = await open(page, '/en/jobs/new')

    await page.locator('[data-testid="job-name"]').fill('healthcheck')
    await page.locator('[data-testid="job-mode-interval"]').check()
    await page.locator('[data-testid="job-every"]').fill('10')
    await page.locator('[data-testid="job-every-unit"]').selectOption('s')
    await page.locator('[data-testid="job-url"]').fill('https://api.esempio.test/health')
    await page.locator('[data-testid="job-submit"]').click()

    await expect.poll(() => written.length).toBe(1)
    expect(written[0]!.body).toMatchObject({ schedule: null, every: '10s' })
  })
})

test.describe('modifica', () => {
  test('carica il job nel modulo', async ({ page }) => {
    await open(page, '/en/jobs/0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c')

    await expect(page.locator('[data-testid="job-name"]')).toHaveValue('daily-digest')
    await expect(page.locator('[data-testid="job-schedule"]')).toHaveValue('0 9 * * *')
    await expect(page.locator('[data-testid="job-timezone"]')).toHaveValue('Europe/Rome')
    await expect(page.locator('[data-testid="job-timeout"]')).toHaveValue('30')
  })

  test('un job che viene da un repository si dice prima, non con un 409', async ({ page }) => {
    /*
     * R13: la riconciliazione riporterebbe indietro qualunque modifica fatta
     * da qui, e la modifica sparirebbe **senza un errore**. L'unica cosa che
     * resta modificabile è la pausa, che il backend tiene distinta apposta.
     */
    await open(page, '/en/jobs/0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c', {
      jobs: [job({ repository_id: 'r1' })],
    })

    await expect(page.locator('[data-testid="job-managed-notice"]')).toBeVisible()
    await expect(page.locator('[data-testid="job-name"]')).toBeDisabled()
    await expect(page.locator('[data-testid="job-schedule"]')).toBeDisabled()
    await expect(page.locator('[data-testid="job-enabled"]')).toBeEnabled()
    await expect(page.locator('[data-testid="job-managed"]')).toBeVisible()
  })
})

// --------------------------------------------------------------- multilingua

test.describe('multilingua', () => {
  test('la sezione esiste in tutte e cinque le lingue', async ({ page }) => {
    // I tipi legano le voci di navigazione ai file di traduzione, quindi una
    // sezione senza traduzioni non compila. Qui si verifica il gradino dopo:
    // che le rotte prefissate portino davvero alla schermata tradotta.
    const attese: Record<string, string> = {
      en: 'Cron jobs',
      it: 'Cronjob',
      es: 'Cron jobs',
      de: 'Cronjobs',
      fr: 'Tâches cron',
    }

    await mockBackend(page, true)
    await mockJobs(page)

    for (const [locale, titolo] of Object.entries(attese)) {
      await page.goto(`/${locale}/jobs`)
      await expect(page.locator('h1')).toHaveText(titolo)
    }
  })
})
