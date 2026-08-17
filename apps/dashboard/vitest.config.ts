import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'

// I test di questa app coprono logica pura e la struttura dei file: nessun
// runtime Nuxt, quindi nessun ambiente browser da simulare. Il comportamento
// dell'interfaccia — rilevamento della lingua, selettore, persistenza — si
// verifica in un browser vero, negli e2e Playwright (e2e/dashboard.spec.ts):
// un test di componente che finge `navigator.languages` e `localStorage`
// proverebbe il finto, non la dashboard generata.
const APP_ROOT = dirname(fileURLToPath(import.meta.url))

export default defineConfig({
  test: {
    environment: 'node',
    include: ['test/**/*.test.ts'],
  },

  // L'alias `~` che Nuxt dà al codice dell'app, così i sorgenti restano
  // idiomatici e i test li importano con lo stesso percorso.
  resolve: {
    alias: {
      '~': APP_ROOT,
      '@': APP_ROOT,
    },
  },
})
