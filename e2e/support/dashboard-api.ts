import type { Page, Route } from '@playwright/test'

/**
 * Il backend Go, finto, per i test della dashboard.
 *
 * Gli e2e servono l'output di `nuxt generate` da un server di file statici
 * (vedi `playwright.config.ts`): nessun backend gira. Ogni richiesta va quindi
 * intercettata, e le due che partono *sempre* — la sessione e l'health check —
 * stanno qui perché altrimenti ogni test finirebbe per misurare il loro
 * fallimento invece di ciò che vuole misurare.
 *
 * Il finto non è una comodità: è ciò che rende verificabili proprio i casi che
 * un backend vero rende scomodi — la sessione che scade a metà navigazione, il
 * backend irraggiungibile — perché qui si decide quando succedono.
 */

/** Risposta che il backend Go dà a `/healthz` quando sta bene. */
export const HEALTHY = { status: 'ok', env: 'test', version: '0.0.0-test' }

/** Utente collegato, nella forma di `UserResponse`. */
export const USER = {
  id: '11111111-1111-4111-8111-111111111111',
  email: 'mario.rossi@example.com',
  full_name: 'Mario Rossi',
  role: 'user',
  timezone: 'Europe/Rome',
  email_verified: true,
  created_at: '2026-01-01T00:00:00Z',
}

/** Involucro di `/auth/session` e di `/auth/login` (`SessionEnvelope`). */
export const SESSION = {
  user: USER,
  session: {
    id: '22222222-2222-4222-8222-222222222222',
    created_at: '2026-08-17T08:00:00Z',
    last_used_at: '2026-08-17T08:00:00Z',
    expires_at: '2026-09-17T08:00:00Z',
    current: true,
  },
}

/** Errore nella forma di `ErrorBody`. */
export function apiError(code: string, message = ''): string {
  return JSON.stringify({ error: { code, message } })
}

/**
 * Governa la sessione che il backend finto dichiara.
 *
 * È un oggetto e non un parametro perché quello che i test interessanti fanno è
 * **cambiarla a metà**: `revoke()` è la sessione che scade o che un altro
 * dispositivo revoca mentre l'utente sta lavorando.
 */
export interface SessionControl {
  /** Fa rispondere 401 a tutto ciò che richiede una sessione, da ora in poi. */
  revoke: () => void
  /** Rimette una sessione valida: è quello che fa un accesso riuscito. */
  restore: () => void
}

/**
 * Installa il backend finto: `/auth/session`, `/auth/logout` e `/healthz`.
 *
 * Le rotte registrate dopo hanno la precedenza su queste, quindi un test che
 * vuole provare un guasto specifico sovrascrive la sua con un `page.route()`
 * proprio senza doverle disattivare tutte.
 *
 * @param authenticated se la sessione è valida all'apertura della pagina
 */
export async function mockBackend(page: Page, authenticated: boolean): Promise<SessionControl> {
  let live = authenticated

  await page.route('**/healthz', route => route.fulfill({ json: HEALTHY }))

  await page.route('**/auth/session', (route: Route) => {
    if (!live) {
      return route.fulfill({ status: 401, body: apiError('unauthenticated'), contentType: 'application/json' })
    }
    return route.fulfill({ json: SESSION })
  })

  await page.route('**/auth/logout', (route: Route) => {
    live = false
    return route.fulfill({ status: 204, body: '' })
  })

  return {
    revoke: () => { live = false },
    restore: () => { live = true },
  }
}
