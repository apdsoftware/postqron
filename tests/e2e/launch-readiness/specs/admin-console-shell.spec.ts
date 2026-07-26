import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'
import {
  covers,
  fixtureReset,
  offBaseURL,
  session,
} from '../helpers.ts'

test.beforeEach(async () => {
  await fixtureReset()
})

const sections = [
  { path: '/admin', heading: /postqron administration/iu },
  { path: '/admin/users', heading: /^users$/iu },
  { path: '/admin/workspaces', heading: /^workspaces$/iu },
  { path: '/admin/plans', heading: /^plans$/iu },
  { path: '/admin/audit', heading: /^audit$/iu },
  { path: '/admin/profile', heading: /^profile$/iu },
] as const

test('every admin section has a distinct deep-linkable URL with the active sidebar entry marked', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN')

  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()

  for (const section of sections) {
    const response = await page.goto(`${offBaseURL}${section.path}`)
    expect(response?.status(), section.path).toBe(200)
    await expect(page.getByRole('heading', { level: 1 })).toContainText(section.heading)
    const activeLink = page.locator('nav a[aria-current="page"]')
    await expect(activeLink).toHaveAttribute('href', section.path)
  }
  await context.close()
})

test('the mobile drawer opens with the menu button and closes with the scrim or Escape', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN')

  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()
  await page.setViewportSize({ width: 375, height: 812 })
  await page.goto(`${offBaseURL}/admin`)

  const sidebar = page.locator('.admin-sidebar')
  const menuButton = page.getByRole('button', { name: 'Open navigation menu' })
  await expect(sidebar).toHaveAttribute('data-open', 'false')

  await menuButton.click()
  await expect(sidebar).toHaveAttribute('data-open', 'true')
  await expect(menuButton).toHaveAttribute('aria-expanded', 'true')

  await page.locator('.admin-shell__scrim').click()
  await expect(sidebar).toHaveAttribute('data-open', 'false')

  await menuButton.click()
  await expect(sidebar).toHaveAttribute('data-open', 'true')
  await page.locator('.admin-sidebar__close').press('Escape')
  await expect(sidebar).toHaveAttribute('data-open', 'false')
  await context.close()
})

test('switching language from the compact select keeps the current admin section', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN', 'LR-I18N')

  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()
  await page.goto(`${offBaseURL}/admin/plans`)

  const select = page.locator('.admin-language-select select')
  await expect(select).toBeVisible()
  await select.selectOption('it')
  await expect(page).toHaveURL(/\/it\/admin\/plans$/u)
  await expect(page.locator('html')).toHaveAttribute('lang', 'it')
  await expect(select).toHaveValue('it')
  await context.close()
})

test('admin sections render without horizontal overflow at 320, 375, 768, and 1280px', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-ADMIN')

  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()

  for (const width of [320, 375, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    for (const section of ['/admin', '/admin/plans']) {
      await page.goto(`${offBaseURL}${section}`)
      const overflow = await page.evaluate(() =>
        document.documentElement.scrollWidth > document.documentElement.clientWidth + 1)
      expect(overflow, `${section} at ${width}px`).toBe(false)
    }
  }
  await context.close()
})

test('every admin section passes serious and critical WCAG checks', async ({
  browser,
}, testInfo) => {
  covers(testInfo, 'LR-WCAG')

  const context = await browser.newContext()
  await session(context, 'admin')
  const page = await context.newPage()

  for (const section of sections) {
    await page.goto(`${offBaseURL}${section.path}`)
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'])
      .analyze()
    const blocking = results.violations.filter(violation =>
      violation.impact === 'serious' || violation.impact === 'critical')
    expect(blocking, section.path).toEqual([])
  }
  await context.close()
})
