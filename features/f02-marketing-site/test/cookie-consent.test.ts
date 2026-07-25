import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = (path: string) => readFile(resolve(featureRoot, path), 'utf8')

function catalogKeys(component: string, locale: string, next: string): string[] {
  const start = component.indexOf(`${' '.repeat(2)}${locale}: {`)
  const end = component.indexOf(next, start)
  assert.notEqual(start, -1, `missing ${locale} cookie catalog`)
  assert.notEqual(end, -1, `unterminated ${locale} cookie catalog`)
  return [...component.slice(start, end).matchAll(/^[ ]{4}([A-Za-z][A-Za-z0-9]*):/gmu)]
    .map(match => match[1]!)
    .sort()
}

test('first-level banner actions are equally prominent and remain explicit opt-in', async () => {
  const component = await source('components/CookiePreferences.vue')

  assert.match(component, /COOKIE_BANNER_FIRST_LEVEL_ACTIONS\.map/)
  assert.match(component, /class="cookie-panel__actions"/)
  assert.equal(
    [...component.matchAll(/class="cookie-action"/gu)].length >= 5,
    true,
  )
  assert.match(
    component,
    /acceptAll\(\)[\s\S]*preferences: true, analytics: true, marketing: true/,
  )
  assert.match(component, /rejectAll\(\)[\s\S]*emptySelection\(\)/)
  assert.match(component, /openCustomization/)
})

test('server state wins over the resilient cache and policy bumps reopen choice', async () => {
  const component = await source('components/CookiePreferences.vue')

  assert.match(component, /requestFetch<unknown>\('\/api\/cookie-preferences'/)
  assert.match(component, /writeCache\(state\)[\s\S]*applyServerState\(state\)/)
  assert.match(component, /show\.value = forceOpen \|\| !state\.has_recorded_choice/)
  assert.match(component, /policyVersion\.value = state\.policy_version/)
  assert.match(
    component,
    /body: \{[\s\S]*policy_version: policyVersion\.value,[\s\S]*source,[\s\S]*\.\.\.next/,
  )
  assert.doesNotMatch(component, /isCurrentChoice/)
})

test('accept, reject, granular updates, revocation, and retries use the API contract', async () => {
  const component = await source('components/CookiePreferences.vue')

  assert.match(component, /saveCustom\(\)[\s\S]*persistChoice\(\{ \.\.\.selection \}, 'preferences_center'\)/)
  assert.match(component, /'Idempotency-Key': key/)
  assert.match(component, /pendingSave\.value = retry/)
  assert.match(
    component,
    /pending[\s\S]*persistChoice\(pending\.selection, pending\.source, pending\.key\)/,
  )
  assert.match(
    component,
    /revokeBeforePersistence\(next\)[\s\S]*await requestFetch<unknown>/,
  )
  assert.match(
    component,
    /activeSelection\.analytics && next\.analytics/,
  )
  assert.match(component, /status === 409[\s\S]*loadFromServer\(true\)/)
})

test('offline behavior is retryable and cannot become implicit consent', async () => {
  const component = await source('components/CookiePreferences.vue')

  assert.match(
    component,
    /catch \{[\s\S]*syncOptionalTechnologies\(emptySelection\(\)\)[\s\S]*show\.value = true[\s\S]*loadError/,
  )
  assert.match(component, /v-if="errorMessage"[\s\S]*role="alert"/)
  assert.match(component, /@click="retry"/)
  assert.match(component, /No new optional technology was enabled/)
})

test('optional technologies have a fail-closed gate and revocation cleanup', async () => {
  const component = await source('components/CookiePreferences.vue')
  const mounted = component.slice(component.indexOf('onMounted(() =>'))

  assert.ok(
    mounted.indexOf('syncOptionalTechnologies(emptySelection())')
      < mounted.indexOf('void loadFromServer()'),
  )
  assert.match(component, /postqronCookieConsent = Object\.freeze/)
  assert.match(component, /allows: \(category: OptionalCookieCategory\)/)
  assert.match(component, /data-postqron-cookie-category/)
  assert.match(component, /element\.remove\(\)/)
  assert.match(component, /Max-Age=0/)
  assert.match(component, /postqron:cookie-preferences-changed/)
  assert.doesNotMatch(component, /<(?:script|img|iframe)[^>]+(?:analytics|marketing)/iu)
})

test('Global Privacy Control prevents marketing activation', async () => {
  const component = await source('components/CookiePreferences.vue')

  assert.match(component, /globalPrivacyControl\?: boolean/)
  assert.match(
    component,
    /marketing: globalPrivacyControl\.value \? false : next\.marketing/,
  )
  assert.match(
    component,
    /category === 'marketing' && globalPrivacyControl/,
  )
})

test('the banner and preferences dialog expose automated accessibility hooks', async () => {
  const component = await source('components/CookiePreferences.vue')

  assert.match(component, /:role="customize \? 'dialog' : 'region'"/)
  assert.match(component, /:aria-modal="customize \? 'true' : undefined"/)
  assert.match(component, /aria-labelledby="cookie-title"/)
  assert.match(component, /aria-describedby="cookie-description"/)
  assert.match(component, /tabindex="-1"/)
  assert.match(component, /@keydown="trapFocus"/)
  assert.match(component, /event\.key !== 'Tab'/)
  assert.match(component, /event\.key === 'Escape'/)
  assert.match(component, /:aria-describedby="`cookie-category-\$\{category\}-description`"/)
  assert.match(component, /role="status"[\s\S]*aria-live="polite"/)
  assert.match(component, /:focus-visible/)
})

test('cookie copy is complete in en, it, es, fr, de with English fallback', async () => {
  const component = await source('components/CookiePreferences.vue')
  const localeBoundaries = [
    ['en', '\n  it: {'],
    ['it', '\n  es: {'],
    ['es', '\n  fr: {'],
    ['fr', '\n  de: {'],
    ['de', '\n  },\n})'],
  ] as const
  const reference = catalogKeys(
    component,
    localeBoundaries[0][0],
    localeBoundaries[0][1],
  )

  for (const boundary of localeBoundaries) {
    assert.deepEqual(catalogKeys(component, boundary[0], boundary[1]), reference)
  }
  assert.match(component, /defineCatalogs\(\{\s*en:/u)
  assert.match(component, /i18n\.locale\.value/)
  assert.match(component, /localizeUrl\(locale\.value, '\/legal\/cookie'\)/)
})

test('GET and PUT proxies preserve the anonymous subject and retry metadata', async () => {
  const [getProxy, putProxy] = await Promise.all([
    source('server/api/cookie-preferences.get.ts'),
    source('server/api/cookie-preferences.put.ts'),
  ])

  for (const proxy of [getProxy, putProxy]) {
    assert.match(proxy, /\$fetch\.raw/)
    assert.match(proxy, /getSetCookie/)
    assert.match(proxy, /appendResponseHeader\(event, 'set-cookie', value\)/)
    assert.match(proxy, /'cache-control', 'no-store'/)
    assert.match(proxy, /\/api\/v1\/cookie-preferences/)
  }
  assert.match(putProxy, /forwardedHeaders\(event\)/)
  assert.match(putProxy, /'idempotent-replay'/)
  assert.match(putProxy, /return response\._data/)
})
