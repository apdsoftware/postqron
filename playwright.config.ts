import { defineConfig, devices } from '@playwright/test'

// Test end-to-end dei due frontend.
//
// Cosa provano che i test unitari non provano. `apps/*/test/` copre utility
// pure; `nuxt generate` può concludersi con successo e produrre comunque un sito
// che non si apre: base URL sbagliata negli asset, idratazione che esplode,
// fallback di routing assente. Il vincolo di distribuzione della SPEC §2 — tutto
// statico, nessun Nitro a runtime — si verifica solo servendo l'output generato
// così com'è e aprendolo in un browser.
//
// Per questo i server non sono `nuxt preview`, che avvia Nitro: sono un server
// di file statici (scripts/static-server.mjs) che emula Cloudflare Pages. Se il
// sito ha bisogno di qualcosa di più, non è statico e il test lo dice.
//
// I test partono da `apps/*/.output/public`: `make e2e` verifica che la build ci
// sia prima di lanciarli.

const WEB_PORT = 4173
const DASHBOARD_PORT = 4174

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,

  // La CI locale deve fallire in modo deterministico: un test che passa solo al
  // secondo tentativo è un test rotto, non un test lento.
  retries: 0,
  forbidOnly: true,

  reporter: [['list']],
  timeout: 15_000,
  expect: { timeout: 5_000 },

  webServer: [
    {
      command: `node scripts/static-server.mjs --root apps/web/.output/public --port ${WEB_PORT}`,
      url: `http://127.0.0.1:${WEB_PORT}/`,
      reuseExistingServer: false,
      timeout: 15_000,
    },
    {
      // --spa replica la regola `/* /index.html 200` di
      // apps/dashboard/public/_redirects.
      command: `node scripts/static-server.mjs --root apps/dashboard/.output/public --port ${DASHBOARD_PORT} --spa`,
      url: `http://127.0.0.1:${DASHBOARD_PORT}/`,
      reuseExistingServer: false,
      timeout: 15_000,
    },
  ],

  // Un solo browser: Chromium. La compatibilità cross-browser si verifica sulle
  // issue di UI, quando esisterà una UI; qui interessa che l'output statico si
  // apra, e tre motori per la stessa asserzione triplicherebbero il tempo della
  // CI senza dire nulla di nuovo.
  projects: [
    {
      name: 'web',
      testMatch: /web\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], baseURL: `http://127.0.0.1:${WEB_PORT}` },
    },
    {
      name: 'dashboard',
      // Tutti i file `dashboard*.spec.ts`: l'autenticazione ne ha uno suo,
      // perché ha bisogno del caso «senza sessione» che negli altri sarebbe
      // rumore in ogni `beforeEach`.
      //
      // `[a-z-]*` e non `.*`: il confronto avviene sul percorso assoluto, e un
      // `.` che attraversa gli slash farebbe corrispondere anche `web.spec.ts`
      // non appena una cartella dell'albero si chiama «dashboard» — cosa che
      // succede, perché i worktree hanno il nome della issue.
      testMatch: /dashboard[a-z-]*\.spec\.ts$/,
      use: { ...devices['Desktop Chrome'], baseURL: `http://127.0.0.1:${DASHBOARD_PORT}` },
    },
  ],
})
