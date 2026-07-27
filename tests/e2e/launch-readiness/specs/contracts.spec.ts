import { expect, test } from '@playwright/test'
import { spawnSync } from 'node:child_process'
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises'
import { dirname, extname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { covers, locales } from '../helpers.ts'

const suiteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repositoryRoot = resolve(suiteRoot, '../../..')

function goContract(directory: string, pattern: string): string {
  const result = spawnSync(
    'go',
    ['test', '-race', '-count=1', '-run', pattern, './...'],
    {
      cwd: resolve(repositoryRoot, directory),
      env: { ...process.env, GOWORK: 'off' },
      encoding: 'utf8',
      timeout: 180_000,
    },
  )
  expect(
    result.status,
    `${directory}\n${result.stdout}\n${result.stderr}`,
  ).toBe(0)
  return `${result.stdout}\n${result.stderr}`
}

test('authoritative Paddle contracts verify catalog, checkout, webhook, entitlement and portal guards', async ({}, testInfo) => {
  covers(testInfo, 'LR-PADDLE', 'LR-NEGATIVE')
  goContract(
    'features/f10-entitlements',
    'Test(PublicCatalogMatchesD09|CheckoutUsesOwnerAndServerComposedPaddleItems|CheckoutRejectsFreePlanAndNonOwner|CustomerPortalIsGeneratedOnDemandFromStoredBinding|VerifyPaddleSignatureUsesRawBodyAndRejectsReplay|PaddleTransactionCompletedIsVerifiedDeduplicatedAndOrdered|PaddleRejectsWrongItemsAndClientCheckoutEventCannotGrant)$',
  )
})

test('authoritative admin contracts enforce 403, allowlist, CSRF and audited mutations', async ({}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-NEGATIVE')
  goContract(
    'features/f31-admin-console',
    'Test(UnauthorizedRequestsReturnBeforeAdminDataIsRead|AdminEndToEndLoginAssignAndRevokeInternalPlan|HTTPRejectsCSRFStaleReauthAndPayloadManipulation|AuthorizationRequiresValidVerifiedAllowlistedActiveSession|SensitiveMutationChecksCSRFReauthConfirmationAndIdempotency)$',
  )
})

test('Mailronix fake contract and event-template-locale-provider matrix are complete', async ({}, testInfo) => {
  covers(testInfo, 'LR-EMAIL', 'LR-LOCALE-MATRIX', 'LR-NEGATIVE')
  const output = goContract(
    'features/f14-email',
    'Test(NoEmailProviderClientExistsOutsideF14|TransactionalEventMatrixIsCompleteAndVersioned|MailronixContractAuthenticationAndSerialization|MailronixMapsDocumentedErrorsAndRedactsDiagnostics|FakeSenderCannotReachRealRecipients|EveryTemplateRendersAllLocalesWithCompleteAlternatives|LocaleFallbackReplayAndLocalizedValues)$',
  )
  for (const locale of locales) {
    expect(output).not.toContain(`missing locale ${locale}`)
  }
})

test('no provider or SMTP client exists outside the F14 transactional boundary', async ({}, testInfo) => {
  covers(testInfo, 'LR-EMAIL', 'LR-SECURITY')
  const prohibited = [
    /api\.mailronix\.com/iu,
    new RegExp(['mailronix', 'client'].join(''), 'iu'),
    /["']net\/smtp["']/iu,
    /smtp\.sendmail/iu,
  ]
  const extensions = new Set(['.go', '.js', '.mjs', '.ts', '.tsx', '.vue'])
  const violations: string[] = []

  async function walk(path: string): Promise<void> {
    for (const entry of await readdir(path, { withFileTypes: true })) {
      if (['.git', '.context', 'node_modules', 'vendor'].includes(entry.name)) {
        continue
      }
      const child = resolve(path, entry.name)
      if (entry.isDirectory()) {
        await walk(child)
        continue
      }
      if (!extensions.has(extname(child))) {
        continue
      }
      if (child.startsWith(resolve(repositoryRoot, 'features/f14-email'))) {
        continue
      }
      const content = await readFile(child, 'utf8')
      if (prohibited.some(pattern => pattern.test(content))) {
        violations.push(child.slice(repositoryRoot.length + 1))
      }
    }
  }
  await walk(repositoryRoot)
  expect(violations).toEqual([])
})

test('locale resolver contract locks URL, profile, cookie, Accept-Language and English fallback precedence', async ({}, testInfo) => {
  covers(testInfo, 'LR-I18N', 'LR-NEGATIVE')
  const { resolveLocale } = await import(
    '../../../../features/f36-i18n/src/resolver.ts'
  )

  expect(resolveLocale({
    url: '/de/app',
    profile: 'fr',
    cookie: 'it',
    acceptLanguage: 'es',
  })).toMatchObject({ locale: 'de', source: 'url' })
  expect(resolveLocale({
    url: '/app',
    profile: 'fr',
    cookie: 'it',
    acceptLanguage: 'es',
  })).toMatchObject({ locale: 'fr', source: 'profile' })
  expect(resolveLocale({
    url: '/app',
    cookie: 'it',
    acceptLanguage: 'es',
  })).toMatchObject({ locale: 'it', source: 'cookie' })
  expect(resolveLocale({
    url: '/app',
    acceptLanguage: 'es-MX,fr;q=0.8',
  })).toMatchObject({ locale: 'es', source: 'browser' })
  expect(resolveLocale({
    url: '/app',
    acceptLanguage: 'pt-BR',
  })).toMatchObject({ locale: 'en', source: 'fallback' })
})

test('requirement matrix gate fails a superficially green but incomplete collection', async ({}, testInfo) => {
  covers(testInfo, 'LR-NEGATIVE')
  const artifactRoot = await mkdtemp(
    join(tmpdir(), 'postqron-launch-matrix-gate-'),
  )
  try {
    const { default: LaunchReadinessReporter } = await import('../reporter.ts')
    const reporter = new LaunchReadinessReporter({ artifactRoot })
    reporter.onBegin({} as never, {} as never)
    reporter.onTestEnd({
      annotations: [{
        type: 'requirement',
        description: 'LR-PRELAUNCH',
      }],
      titlePath: () => ['partial collection fixture'],
      location: { file: 'partial.fixture.ts' },
    } as never, {
      status: 'passed',
      duration: 1,
    } as never)

    const outcome = await reporter.onEnd({ status: 'passed' } as never)
    const report = JSON.parse(await readFile(
      join(artifactRoot, 'launch-readiness-report.json'),
      'utf8',
    )) as {
      overallStatus: string
      matrix: Array<{ id: string, status: string }>
    }

    expect(outcome.status).toBe('failed')
    expect(report.overallStatus).toBe('failed')
    expect(report.matrix.find(row => row.id === 'LR-PRELAUNCH')?.status)
      .toBe('passed')
    expect(report.matrix.find(row => row.id === 'LR-LEGAL')?.status)
      .toBe('missing')
  } finally {
    await rm(artifactRoot, { recursive: true, force: true })
  }
})
