import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

test('AppState and app entry expose the unavailable service state separately from offline', async () => {
  const [state, app] = await Promise.all([
    readFile(new URL('../components/AppState.vue', import.meta.url), 'utf8'),
    readFile(new URL('../pages/app.vue', import.meta.url), 'utf8'),
  ])

  assert.match(state, /'unavailable'/u)
  assert.match(state, /state\.\$\{messageKey\}\.title/u)
  assert.match(app, /state === 'offline' \|\| state === 'access-denied' \|\| state === 'unavailable'/u)
  assert.match(app, /requestedState === 'unavailable'[\s\S]*kind="unavailable"/u)
})
