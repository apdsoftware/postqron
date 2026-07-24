import assert from 'node:assert/strict'
import { access, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import test from 'node:test'
import { fileURLToPath, URL } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))

async function source(path) {
  return readFile(join(root, path), 'utf8')
}

function luminance(hex) {
  const channels = hex
    .replace('#', '')
    .match(/.{2}/g)
    .map(channel => Number.parseInt(channel, 16) / 255)
    .map(channel => channel <= 0.04045
      ? channel / 12.92
      : ((channel + 0.055) / 1.055) ** 2.4)

  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

function contrast(first, second) {
  const values = [luminance(first), luminance(second)].sort((a, b) => b - a)
  return (values[0] + 0.05) / (values[1] + 0.05)
}

test('slice manifest is discoverable and local', async () => {
  const manifest = await source('feature.yaml')

  assert.match(manifest, /^schema_version: 1$/m)
  assert.match(manifest, /^id: brand$/m)
  assert.match(manifest, /^kind: web$/m)
  assert.match(manifest, /^entrypoint: \.\/runtime\.ts$/m)
  await access(join(root, 'runtime.ts'))
})

test('required brand assets are present, scalable and accessible', async () => {
  const assets = [
    'mark.svg',
    'favicon.svg',
    'logo-primary.svg',
    'logo-reversed.svg',
    'logo-monochrome.svg',
    'app-icon.svg',
    'social-card.svg',
  ]

  for (const asset of assets) {
    const svg = await source(`assets/${asset}`)
    assert.match(svg, /^<svg[^>]+viewBox=/)
    assert.match(svg, /role="img"/)
    assert.match(svg, /(?:aria-label|<title)/)
    assert.doesNotMatch(svg, /<script|javascript:/i)
  }
})

test('token source covers color, type, layout, motion and layering', async () => {
  const tokens = JSON.parse(await source('tokens/tokens.json'))
  const requiredGroups = [
    'color',
    'font',
    'space',
    'size',
    'radius',
    'border',
    'shadow',
    'duration',
    'easing',
    'breakpoint',
    'zIndex',
  ]

  for (const group of requiredGroups) {
    assert.ok(tokens[group], `missing token group: ${group}`)
  }
  assert.ok(tokens.color.semantic.light)
  assert.ok(tokens.color.semantic.dark)
})

test('semantic text pairs meet WCAG AA contrast in both themes', async () => {
  const tokens = JSON.parse(await source('tokens/tokens.json'))
  const themes = tokens.color.semantic
  const pairs = {
    light: [
      ['text', 'canvas'],
      ['text', 'surface'],
      ['textMuted', 'surface'],
      ['textInverse', 'brand'],
      ['onDanger', 'dangerSolid'],
      ['danger', 'dangerSurface'],
      ['warning', 'warningSurface'],
      ['info', 'infoSurface'],
      ['success', 'successSurface'],
    ],
    dark: [
      ['text', 'canvas'],
      ['text', 'surface'],
      ['textMuted', 'surface'],
      ['textInverse', 'brand'],
      ['onDanger', 'dangerSolid'],
      ['danger', 'dangerSurface'],
      ['warning', 'warningSurface'],
      ['info', 'infoSurface'],
      ['success', 'successSurface'],
    ],
  }

  for (const [theme, themePairs] of Object.entries(pairs)) {
    for (const [foreground, background] of themePairs) {
      const ratio = contrast(
        themes[theme][foreground].$value,
        themes[theme][background].$value,
      )
      assert.ok(
        ratio >= 4.5,
        `${theme} ${foreground}/${background} contrast ${ratio.toFixed(2)} is below 4.5`,
      )
    }
  }
})

test('focus indicators contrast with adjacent surfaces', async () => {
  const tokens = JSON.parse(await source('tokens/tokens.json'))

  for (const theme of ['light', 'dark']) {
    const semantic = tokens.color.semantic[theme]
    const ratio = contrast(semantic.focus.$value, semantic.surface.$value)
    assert.ok(ratio >= 3, `${theme} focus contrast ${ratio.toFixed(2)} is below 3`)
  }
})

test('component styles preserve focus, target size and user preferences', async () => {
  const css = await source('components/components.css')
  const tokenCss = await source('tokens/tokens.css')

  assert.match(css, /:focus-visible/)
  assert.match(css, /min-height:\s*var\(--pq-size-target-min\)/)
  assert.match(css, /@media \(forced-colors: active\)/)
  assert.match(tokenCss, /@media \(prefers-reduced-motion: reduce\)/)
  assert.match(tokenCss, /\[data-pq-theme="dark"\]/)
  assert.match(tokenCss, /\[data-pq-theme="system"\]/)
})

test('interactive components expose native semantics and accessible state', async () => {
  const button = await source('components/PqButton.vue')
  const field = await source('components/PqField.vue')
  const alert = await source('components/PqAlert.vue')
  const skipLink = await source('components/PqSkipLink.vue')

  assert.match(button, /<button/)
  assert.match(button, /:aria-busy=/)
  assert.match(field, /<label/)
  assert.match(field, /:aria-describedby=/)
  assert.match(field, /:aria-invalid=/)
  assert.match(alert, /role="liveRole"/)
  assert.match(alert, /:aria-label="dismissLabel"/)
  assert.match(skipLink, /href: '#main-content'/)
})
