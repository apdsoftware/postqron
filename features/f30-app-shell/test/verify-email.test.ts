import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  APP_SHELL_CATALOGS,
  APP_SHELL_LOCALES,
} from '../components/core/catalogs.ts'
import {
  completeEmailVerification,
  emailVerificationDataKey,
  requestEmailVerification,
  withoutEmailVerificationToken,
  withoutEmailVerificationTokenInHistoryState,
} from '../components/core/email-verification.ts'

test('successful verification resend clears the email field', async () => {
  let requestedEmail = ''
  const result = await requestEmailVerification(
    'person@example.com',
    async (email) => {
      requestedEmail = email
    },
  )

  assert.equal(requestedEmail, 'person@example.com')
  assert.deepEqual(result, { email: '', status: 'success' })
})

test('failed verification resend preserves the email field for retry', async () => {
  const result = await requestEmailVerification(
    'person+correction@example.com',
    async () => {
      throw new Error('network unavailable')
    },
  )

  assert.deepEqual(result, {
    email: 'person+correction@example.com',
    status: 'error',
  })
})

test('email verification invokes the one-time operation exactly once', async () => {
  let executions = 0
  let receivedToken = ''

  const result = await completeEmailVerification(
    '  one-time-secret  ',
    async (token) => {
      executions += 1
      receivedToken = token
    },
  )

  assert.equal(result, 'verified')
  assert.equal(executions, 1)
  assert.equal(receivedToken, 'one-time-secret')
})

test('email verification skips an absent token and serializes failures as state', async () => {
  let executions = 0
  const verify = async () => {
    executions += 1
    throw new Error('consumed or invalid')
  }

  assert.equal(await completeEmailVerification(' ', verify), 'no-token')
  assert.equal(executions, 0)
  assert.equal(await completeEmailVerification('token', verify), 'invalid')
  assert.equal(executions, 1)
})

test('verification cleanup removes only the token without executing verification again', async () => {
  let executions = 0
  const secret = 'one-time-secret'
  const result = await completeEmailVerification(secret, async () => {
    executions += 1
  })
  const safeLocation = withoutEmailVerificationToken(
    `https://postqron.test/it/app/verify-email?token=${secret}&plan=pro&return_to=%2Fit%2Fapp%2Fhome#result`,
  )
  const historyState = withoutEmailVerificationTokenInHistoryState(
    {
      back: '/it/app',
      current: `/it/app/verify-email?token=${secret}&plan=pro`,
      position: 2,
    },
    safeLocation,
  )

  assert.equal(executions, 1)
  assert.equal(result, 'verified')
  assert.equal(
    safeLocation,
    '/it/app/verify-email?plan=pro&return_to=%2Fit%2Fapp%2Fhome#result',
  )
  assert.doesNotMatch(JSON.stringify(result), /one-time-secret/u)
  assert.doesNotMatch(safeLocation, /token|one-time-secret/u)
  assert.deepEqual(historyState, {
    back: '/it/app',
    current: safeLocation,
    position: 2,
  })
  assert.doesNotMatch(JSON.stringify(historyState), /token|one-time-secret/u)
})

test('later SPA visits receive distinct token-free async-data keys', () => {
  const firstVisit = emailVerificationDataKey('v-1')
  const laterVisit = emailVerificationDataKey('v-2')

  assert.notEqual(firstVisit, laterVisit)
  assert.doesNotMatch(firstVisit, /first-secret/u)
  assert.doesNotMatch(laterVisit, /later-secret/u)
})

test('verification page hydrates a serialized result without exposing the token', async () => {
  const page = await readFile(
    new URL('../pages/verify-email.vue', import.meta.url),
    'utf8',
  )

  assert.match(
    page,
    /verificationDataKey = emailVerificationDataKey\(useId\(\)\)[\s\S]*await useAsyncData\(\s*verificationDataKey,[\s\S]*completeEmailVerification\(/u,
  )
  assert.equal(
    page.match(/candidate => api\.verifyEmail\(candidate\)/gu)?.length,
    2,
  )
  assert.doesNotMatch(
    page,
    /if \(token\.value\)[\s\S]*await api\.verifyEmail\(token\.value\)/u,
  )
  assert.doesNotMatch(
    page,
    /emailVerificationDataKey\((?:token\.value|route\.query)/u,
  )
  assert.match(page, /initialToken = ''/u)
  assert.doesNotMatch(
    page,
    /localStorage|sessionStorage|console\.(?:log|debug|info)/u,
  )
  assert.doesNotMatch(
    page,
    /useHead\(computed\(\(\) => \(\{[^}]*token/u,
  )
  assert.match(
    page,
    /function removeTokenFromHistory\(\) \{[\s\S]*withoutEmailVerificationToken\(globalThis\.location\.href\)[\s\S]*history\.replaceState/u,
  )
  assert.match(
    page,
    /onMounted\(\(\) => \{\s*removeTokenFromHistory\(\)\s*\}\)/u,
  )
  assert.match(
    page,
    /watch\(\(\) => route\.query\.token, async \(value\) => \{[\s\S]*completeEmailVerification\([\s\S]*candidate => api\.verifyEmail\(candidate\)[\s\S]*removeTokenFromHistory\(\)/u,
  )
  assert.doesNotMatch(page, /watch\([^)]*route\.query\.token[\s\S]*immediate: true/u)
  const template = page.slice(page.indexOf('<template>'))
  assert.doesNotMatch(template, /token|verificationDataKey/u)
})

test('verification and post-registration markup expose only valid state actions', async () => {
  const [verificationPage, authPage, styles] = await Promise.all([
    readFile(new URL('../pages/verify-email.vue', import.meta.url), 'utf8'),
    readFile(new URL('../pages/app.vue', import.meta.url), 'utf8'),
    readFile(new URL('../components/app-shell.css', import.meta.url), 'utf8'),
  ])

  assert.match(
    verificationPage,
    /v-if="verification !== 'verified'"[\s\S]*class="email-verification__form"/u,
  )
  assert.match(
    verificationPage,
    /v-else[\s\S]*class="email-verification__success-actions"[\s\S]*appRoute\(locale, 'entry'\)/u,
  )
  assert.match(verificationPage, /aria-live="polite"/u)
  assert.match(verificationPage, /autocomplete="email"/u)

  assert.doesNotMatch(authPage, /auth\.verificationOpen/u)
  assert.doesNotMatch(
    authPage,
    /requestedVerification[\s\S]*appRoute\(locale, 'verify-email'\)/u,
  )
  assert.match(
    authPage,
    /class="auth-verification__actions"[\s\S]*data-full-width="true"[\s\S]*resendVerification/u,
  )
  assert.doesNotMatch(
    authPage,
    /providers\.length|auth-provider|api\.authorize/u,
  )

  assert.match(
    styles,
    /\.auth-verification__actions,[\s\S]*grid-template-columns: minmax\(0, 1fr\)/u,
  )
  assert.match(
    styles,
    /\.email-verification__form \.pq-button,[\s\S]*width: 100%[\s\S]*white-space: normal/u,
  )
})

test('all locales distinguish verified and resent outcomes', () => {
  for (const locale of APP_SHELL_LOCALES) {
    const catalog = APP_SHELL_CATALOGS[locale]
    assert.ok(catalog['verify.success'])
    assert.ok(catalog['verify.resent'])
    assert.notEqual(catalog['verify.success'], catalog['verify.resent'])
    assert.match(
      catalog['verify.resent'],
      /privacy|privacy|privacidad|vie privée|Privatsphäre/u,
    )
    assert.match(
      catalog['verify.resent'],
      /already verified|già verificato|ya está verificada|déjà vérifié|bereits bestätigt/u,
    )
    assert.equal('auth.verificationOpen' in catalog, false)
    assert.doesNotMatch(JSON.stringify(catalog), /one-time-secret/u)
  }
})
