import type {
  FullConfig,
  FullResult,
  Reporter,
  Suite,
  TestCase,
  TestResult,
} from '@playwright/test/reporter'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

interface Requirement {
  id: string
  description: string
}

interface TestRecord {
  title: string
  file: string
  requirements: string[]
  status: string
  durationMs: number
  error?: string
}

const root = dirname(fileURLToPath(import.meta.url))
const defaultArtifactRoot = resolve(root, 'artifacts')
const requirements = JSON.parse(
  readFileSync(resolve(root, 'requirements.json'), 'utf8'),
) as Requirement[]

function clean(value: string): string {
  return value
    .replaceAll(root, '<suite>')
    .replaceAll(process.cwd(), '<repo>')
    .replace(
      /(Bearer|token|secret|password|api[_-]?key)\s*[=:]\s*[^\s"'<>]+/giu,
      '$1=<redacted>',
    )
    .replace(
      /\b[A-Z0-9._%+-]+@(?!example\.(?:test|invalid)\b)[A-Z0-9.-]+\.[A-Z]{2,}\b/giu,
      '<redacted-email>',
    )
    .slice(0, 2_000)
}

class LaunchReadinessReporter implements Reporter {
  private records: TestRecord[] = []
  private artifactRoot: string

  constructor(options: { artifactRoot?: string } = {}) {
    this.artifactRoot = options.artifactRoot || defaultArtifactRoot
  }

  onBegin(_config: FullConfig, _suite: Suite): void {
    mkdirSync(this.artifactRoot, { recursive: true })
  }

  onTestEnd(test: TestCase, result: TestResult): void {
    const requirementIDs = test.annotations
      .filter(annotation => annotation.type === 'requirement')
      .flatMap(annotation => (annotation.description || '').split(','))
      .map(value => value.trim())
      .filter(Boolean)
    this.records.push({
      title: clean(test.titlePath().filter(Boolean).join(' › ')),
      file: clean(test.location.file),
      requirements: requirementIDs,
      status: result.status,
      durationMs: result.duration,
      error: result.error?.message ? clean(result.error.message) : undefined,
    })
  }

  async onEnd(
    result: FullResult,
  ): Promise<{ status?: FullResult['status'] }> {
    const matrix = requirements.map(requirement => {
      const tests = this.records.filter(record =>
        record.requirements.includes(requirement.id))
      const status = tests.length === 0
        ? 'missing'
        : tests.every(test => test.status === 'passed')
          ? 'passed'
          : 'failed'
      return { ...requirement, status, tests }
    })
    const matrixPassed = matrix.every(requirement =>
      requirement.status === 'passed')
    const effectiveStatus = matrixPassed ? result.status : 'failed'
    const generatedAt = new Date().toISOString()
    const payload = {
      generatedAt,
      overallStatus: effectiveStatus,
      matrix,
      manualChecklist: 'manual-checklist.md',
    }
    writeFileSync(
      resolve(this.artifactRoot, 'launch-readiness-report.json'),
      `${JSON.stringify(payload, null, 2)}\n`,
    )
    const lines = [
      '# Launch readiness report',
      '',
      `Generated: ${generatedAt}`,
      `Playwright result: **${effectiveStatus}**`,
      '',
      '| Requirement | Status | Automated tests |',
      '| --- | --- | --- |',
      ...matrix.map(row =>
        `| ${row.id} — ${row.description.replaceAll('|', '\\|')} | ${row.status} | ${
          row.tests.map(test => test.title.replaceAll('|', '\\|')).join('<br>') || '—'
        } |`),
      '',
      'The production-only checks are recorded in `manual-checklist.md`.',
      '',
    ]
    writeFileSync(
      resolve(this.artifactRoot, 'launch-readiness-report.md'),
      lines.join('\n'),
    )
    return { status: effectiveStatus }
  }
}

export default LaunchReadinessReporter
