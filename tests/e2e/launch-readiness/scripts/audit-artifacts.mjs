import { readFile, readdir, stat } from 'node:fs/promises'
import { dirname, extname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const targets = [
  resolve(root, 'artifacts'),
  resolve(root, 'fixtures'),
  resolve(root, 'specs'),
]
const textExtensions = new Set([
  '.css', '.html', '.json', '.log', '.md', '.mjs', '.txt', '.ts', '.xml',
])
const violations = []

async function walk(path) {
  let metadata
  try {
    metadata = await stat(path)
  } catch {
    return
  }
  if (metadata.isDirectory()) {
    for (const entry of await readdir(path)) {
      await walk(resolve(path, entry))
    }
    return
  }
  if (!textExtensions.has(extname(path))) {
    return
  }
  const content = await readFile(path, 'utf8')
  const credential = /(?:pdl_(?:live|sandbox)_[A-Za-z0-9]+|sk_(?:live|test)_[A-Za-z0-9]+|Bearer\s+[A-Za-z0-9._~+/=-]{12,}|["']password["']\s*:\s*["'][A-Za-z0-9._~+/=-]{8,}["'])/giu
  if (credential.test(content)) {
    violations.push(`${path}: credential-shaped value`)
  }
  const emails = content.match(
    /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/giu,
  ) || []
  for (const email of emails) {
    const lower = email.toLowerCase()
    if (!lower.endsWith('@example.test')
      && !lower.endsWith('@example.invalid')
      && lower !== 'help@postqron.com') {
      violations.push(`${path}: unexpected email address`)
    }
  }
}

for (const target of targets) {
  await walk(target)
}
if (violations.length) {
  process.stderr.write(`${violations.join('\n')}\n`)
  process.exit(1)
}
process.stdout.write('launch-readiness artifacts contain no credential or PII markers\n')
