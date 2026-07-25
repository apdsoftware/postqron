import type { BrowserContext, Page, TestInfo } from '@playwright/test'

export const offBaseURL = process.env.LAUNCH_BASE_URL
  || 'http://127.0.0.1:41795'
export const onBaseURL = process.env.LAUNCH_PRELAUNCH_URL
  || 'http://127.0.0.1:41796'
export const fixtureBaseURL = process.env.LAUNCH_FIXTURE_URL
  || 'http://127.0.0.1:41797'

export const locales = ['en', 'it', 'es', 'fr', 'de'] as const

export function localized(locale: typeof locales[number], path: string): string {
  return `${locale === 'en' ? '' : `/${locale}`}${path}`
}

export function covers(testInfo: TestInfo, ...requirements: string[]): void {
  testInfo.annotations.push({
    type: 'requirement',
    description: requirements.join(','),
  })
}

export async function fixtureReset(): Promise<void> {
  if (process.env.LAUNCH_BASE_URL && !process.env.LAUNCH_FIXTURE_URL) {
    return
  }
  const response = await fetch(`${fixtureBaseURL}/__fixture/reset`, {
    method: 'POST',
  })
  if (!response.ok) {
    throw new Error(`fixture reset failed with ${response.status}`)
  }
}

export async function session(
  context: BrowserContext,
  role: 'authenticated' | 'normal' | 'admin',
): Promise<void> {
  const url = new URL(offBaseURL)
  await context.addCookies([{
    name: 'postqron_fixture_session',
    value: role,
    domain: url.hostname,
    path: '/',
    httpOnly: true,
    sameSite: 'Lax',
    secure: url.protocol === 'https:',
  }])
}

export function captureDiagnostics(page: Page): {
  console: string[]
  requests: string[]
} {
  const diagnostics = { console: [] as string[], requests: [] as string[] }
  page.on('console', message => diagnostics.console.push(message.text()))
  page.on('request', request => diagnostics.requests.push(request.url()))
  return diagnostics
}

export function assertNoSensitiveDiagnostics(values: readonly string[]): void {
  const joined = values.join('\n')
  if (/(?:pdl_(?:live|sandbox)|sk_(?:live|test)|Bearer\s+\S+|password=)/iu.test(joined)) {
    throw new Error('browser diagnostics contain a credential-shaped value')
  }
  const emails = joined.match(
    /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/giu,
  ) || []
  const unexpected = emails.filter(email =>
    !email.endsWith('@example.test')
    && email.toLowerCase() !== 'help@postqron.com')
  if (unexpected.length) {
    throw new Error('browser diagnostics contain unexpected personal data')
  }
}
